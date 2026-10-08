// RTT graphs pane: one chart per visible target on a shared time axis (the
// browser's counterpart to the TUI's RTT Graphs pane). Hovering any chart
// moves one cursor across all of them, so a slowdown can be compared across
// targets at the same moment.
import { el, statusChip } from "./dom.js";
import { drawChart, drawCrosshair, indexAt } from "./chart.js";
import { formatRTT, rowLevel } from "./model.js";
import { agoLabel, padTo, sharedMax } from "./timeline.js";

const levelLabels = { ok: "OK", warn: "Warn", crit: "Crit", pending: "Waiting" };

/**
 * @param {{grid: HTMLElement, cursorNote: HTMLElement, onSelect: (id: number) => void}} dom
 */
export function createGraphs(dom) {
  const cards = new Map(); // target id -> {root, title, value, canvas, geo}
  let state = { targets: [], history: new Map(), meta: null, points: 0, shared: false, selectedId: null };
  let cursor = -1; // shared sample index into every padded series
  let paintQueued = false;

  const seriesFor = (id) => padTo(state.history.get(id), state.points);

  const paint = () => {
    paintQueued = false;
    if (!state.meta) return;
    const all = state.targets.map((t) => seriesFor(t.id));
    const yMax = state.shared ? sharedMax(all) : undefined;
    state.targets.forEach((t, i) => {
      const card = cards.get(t.id);
      const series = all[i];
      card.geo = drawChart(card.canvas, series, state.meta.thresholds, state.meta.interval_ms, { yMax });
      if (cursor >= 0) drawCrosshair(card.canvas, card.geo, series, cursor);
      const at = cursor >= 0 ? cursor : series.length - 1;
      const v = series[at];
      card.value.textContent = v === null ? "Lost" : v === undefined ? "–" : formatRTT(v);
      card.value.classList.toggle("is-lost", v === null);
    });
    dom.cursorNote.textContent = cursor >= 0 && state.points > 0
      ? `Cursor: ${agoLabel(state.points - 1 - cursor, state.meta.interval_ms)} — hover any chart to compare targets at the same moment`
      : "Hover any chart to compare targets at the same moment.";
  };
  const schedulePaint = () => {
    if (paintQueued) return;
    paintQueued = true;
    requestAnimationFrame(paint);
  };

  const makeCard = (t) => {
    const canvas = el("canvas", { className: "graph-canvas", attrs: { "aria-hidden": "true" } });
    const status = el("span", { className: "graph-status" });
    const title = el("span", { className: "graph-host" });
    const value = el("span", { className: "graph-value" });
    const root = el("div", {
      className: "graph-card",
      attrs: { tabindex: "0", role: "button", "data-target-id": String(t.id) },
    }, el("div", { className: "graph-head" }, status, title, value), canvas);
    const card = { root, status, title, value, canvas, geo: null };
    root.addEventListener("click", () => dom.onSelect(t.id));
    root.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        dom.onSelect(t.id);
      }
    });
    canvas.addEventListener("pointermove", (e) => {
      if (!card.geo || state.points === 0) return;
      cursor = indexAt(card.geo, state.points, e.offsetX);
      schedulePaint();
    });
    canvas.addEventListener("pointerleave", () => {
      cursor = -1;
      schedulePaint();
    });
    return card;
  };

  return {
    /**
     * @param {{targets: object[], history: Map<number, (number|null)[]>, meta: object,
     *          points: number, shared: boolean, selectedId: number|null}} next
     */
    render(next) {
      state = next;
      const live = new Set(state.targets.map((t) => t.id));
      for (const [id, card] of cards) {
        if (!live.has(id)) {
          card.root.remove();
          cards.delete(id);
        }
      }
      const ordered = state.targets.map((t) => {
        const card = cards.get(t.id) ?? makeCard(t);
        cards.set(t.id, card);
        const level = state.meta ? rowLevel(t, state.meta.thresholds) : "pending";
        card.status.replaceChildren(statusChip(level, levelLabels[level]));
        card.title.textContent = t.host;
        card.title.title = t.host;
        card.root.setAttribute("aria-label", `${t.host} RTT graph, select to inspect`);
        card.root.setAttribute("aria-pressed", String(t.id === state.selectedId));
        return card.root;
      });
      // Reorder without rebuilding: appending an attached node moves it.
      ordered.forEach((node, i) => {
        if (dom.grid.children[i] !== node) dom.grid.insertBefore(node, dom.grid.children[i] ?? null);
      });
      schedulePaint();
    },
    repaint: schedulePaint,
  };
}
