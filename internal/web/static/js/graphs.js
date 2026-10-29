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
import { formatTime } from "./time.js";
import { agoLabel, alignSamples, downsample, padTo, sharedMax } from "./timeline.js";

/**
 * @param {{grid: HTMLElement, cursorNote: HTMLElement, onSelect: (id: number) => void}} dom
 */
export function createGraphs(dom) {
  const cards = new Map(); // target id -> {root, status, title, value, canvas, readout, empty, geo}
  let state = { targets: [], history: new Map(), meta: null, points: 0, shared: true, selectedId: null };
  let cursor = -1; // shared point index into every reduced series
  let reduced = { points: 0, size: 1 }; // shape of the last drawn series
  let lastKey = null;
  let paintQueued = false;
  let announce = null;
  const announcement = el("span", {className:"visually-hidden",attrs:{role:"status","aria-live":"polite"}});
  dom.grid.after(announcement);

  const paint = () => {
    paintQueued = false;
    if (!state.meta || state.targets.length === 0) return;
    const full = state.targets.map((t) => {
      const samples = state.history.get(t.id) ?? [];
      return samples.length && typeof samples[0] === "object" && samples[0] !== null
        ? alignSamples(samples, state.points, state.meta.interval_ms, state.now ?? Date.now())
        : padTo(samples, state.points);
    });
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
      // Numeric values belong to the point being inspected. The monitor
      // already shows latest RTT, so idle cards show only their history.
      const v = cursor >= 0 ? values[cursor] : undefined;
      card.value.textContent = cursor < 0 ? "" : v === null ? "Lost" : v === undefined ? "–" : formatRTT(v);
      card.value.classList.toggle("is-lost", v === null);
      const at = formatTime((state.now ?? Date.now()) - (values.length - 1 - Math.max(cursor,0)) * size * state.meta.interval_ms);
      card.readout.textContent = cursor < 0 ? "Focus chart and use ← / → to inspect samples."
        : `${at} · ${sampleText(v, size, lost[cursor])}`;
      card.readout.title = card.readout.textContent;
      card.empty.hidden = full[i].some(value=>value !== undefined);
      if (announce === t.id) announcement.textContent = `${t.host}: ${card.readout.textContent}`;
    });
    announce = null;
    if (cursor >= 0) {
      const ago = agoLabel((reduced.points - 1 - cursor) * reduced.size, state.meta.interval_ms);
      dom.cursorNote.textContent = `Cursor: ${ago} · ${state.shared ? "Shared scale" : "Individual scales: heights are not comparable"}`;
    } else {
      dom.cursorNote.textContent = state.shared
        ? "Shared scale · hover or use ← / → to compare."
        : "Individual scales · heights are not directly comparable.";
    }
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
    const readout = el("p",{className:"graph-readout",attrs:{id:`graph-readout-${t.id}`}});
    const empty = el("p",{className:"graph-empty",text:"Waiting for the first completed measurement…"});
    const root = el("div", {
      className: "graph-card",
      attrs: { tabindex: "0", role: "button", "data-target-id": String(t.id), "aria-describedby":`graph-readout-${t.id}` },
    }, el("div", { className: "graph-head" }, status, title, value), canvas, empty, readout);
    const card = { root, status, title, value, canvas, readout, empty, geo: null };
    root.addEventListener("click", () => dom.onSelect(t.id));
    root.addEventListener("keydown", (e) => {
      if (e.isComposing || e.ctrlKey || e.metaKey || e.altKey) return;
      if (e.key === "ArrowLeft" || e.key === "ArrowRight" || e.key === "Home" || e.key === "End") {
        e.preventDefault();
        e.stopPropagation();
        if (!reduced.points) return;
        cursor = movedCursor(e.key, cursor, reduced.points);
        announce = t.id;
        schedulePaint();
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        e.stopPropagation();
        cursor = -1;
        schedulePaint();
        return;
      }
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
      if (document.activeElement === root) return;
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
        state.meta && JSON.stringify(state.meta.thresholds), state.meta?.interval_ms, state.now];
      if (!lastKey || key.some((v, i) => v !== lastKey[i])) {
        lastKey = key;
        schedulePaint();
      }
    },
    repaint: schedulePaint,
  };
}

/** Readout for one inspected point; a downsampled point shows its peak. */
function sampleText(value, samplesPerPoint, includesLoss) {
  if (value === undefined) return "No measurement";
  if (value === null) return "No reply";
  return `${samplesPerPoint > 1 ? "Peak RTT" : "RTT"} ${formatRTT(value)}${includesLoss ? " · Includes lost probes" : ""}`;
}

/** Arrow keys step from the cursor (or the newest point); Home/End jump. */
function movedCursor(key, cursor, points) {
  if (key === "Home") return 0;
  if (key === "End") return points - 1;
  const current = cursor < 0 ? points - 1 : cursor;
  return Math.max(0, Math.min(points - 1, current + (key === "ArrowLeft" ? -1 : 1)));
}
