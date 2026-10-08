// RTT graphs pane: one chart per visible target on a shared time axis (the
// browser's counterpart to the TUI's RTT Graphs pane). Hovering any chart
// moves one cursor across all of them, so a slowdown can be compared across
// targets at the same moment.
//
// Charts are drawn from series reduced to about one point per pixel and only
// when what they show changes (new history, window, scale, target set, size or
// cursor), not on every snapshot: with dozens of targets and long windows a
// full redraw per second would cost far more than it shows.
import { el, statusChip } from "./dom.js";
import { drawChart, drawCrosshair, indexAt } from "./chart.js";
import { formatRTT, rowLevel } from "./model.js";
import { agoLabel, downsample, padTo, sharedMax } from "./timeline.js";

/**
 * @param {{grid: HTMLElement, cursorNote: HTMLElement, onSelect: (id: number) => void}} dom
 */
export function createGraphs(dom) {
  const cards = new Map(); // target id -> {root, title, value, canvas, geo}
  let state = { targets: [], history: new Map(), meta: null, points: 0, shared: false, selectedId: null };
  let cursor = -1; // shared point index into every reduced series
  let reduced = { points: 0, size: 1 }; // shape of the last drawn series
  let lastKey = null;
  let paintQueued = false;

  const paint = () => {
    paintQueued = false;
    if (!state.meta || state.targets.length === 0) return;
    const full = state.targets.map((t) => padTo(state.history.get(t.id), state.points));
    const yMax = state.shared ? sharedMax(full) : undefined;
    // Every card has the same width, so one bucket count serves them all and
    // the shared cursor index means the same moment in each chart.
    const width = cards.get(state.targets[0].id).canvas.clientWidth;
    state.targets.forEach((t, i) => {
      const card = cards.get(t.id);
      const { values, lost, size } = downsample(full[i], Math.max(1, Math.floor(width)));
      reduced = { points: values.length, size };
      if (cursor >= values.length) cursor = -1;
      card.geo = drawChart(card.canvas, values, state.meta.thresholds, state.meta.interval_ms,
        { yMax, lost, samplesPerPoint: size });
      if (cursor >= 0) drawCrosshair(card.canvas, card.geo, values, cursor);
      // At the cursor: that point's peak RTT. Otherwise the latest sample
      // itself, matching the monitor's "Last" rather than a bucket peak.
      const v = cursor >= 0 ? values[cursor] : full[i][full[i].length - 1];
      card.value.textContent = v === null ? "Lost" : v === undefined ? "–" : formatRTT(v);
      card.value.classList.toggle("is-lost", v === null);
    });
    dom.cursorNote.textContent = cursor >= 0
      ? `Cursor: ${agoLabel((reduced.points - 1 - cursor) * reduced.size, state.meta.interval_ms)} — hover any chart to compare targets at the same moment`
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
      if (!card.geo || reduced.points === 0) return;
      const next = indexAt(card.geo, reduced.points, e.offsetX);
      if (next !== cursor) {
        cursor = next;
        schedulePaint();
      }
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
        card.status.replaceChildren(statusChip(state.meta ? rowLevel(t, state.meta.thresholds) : "pending"));
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
      // Status chips and selection are DOM; only what the canvases show
      // needs a repaint. A new history fetch replaces the Map, so identity
      // is enough to notice it.
      const key = [state.history, state.points, state.shared, state.targets.map((t) => t.id).join(),
        state.meta && JSON.stringify(state.meta.thresholds), state.meta?.interval_ms];
      if (!lastKey || key.some((v, i) => v !== lastKey[i])) {
        lastKey = key;
        schedulePaint();
      }
    },
    repaint: schedulePaint,
  };
}
