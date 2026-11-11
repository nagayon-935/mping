// The selected host's RTT chart in Summary. Its y axis is robust (top just
// above the window's p95; higher samples are clipped with a "▲ 680 ms"
// marker), and it is the only large chart: the monitor shows sparklines.
//
// Focus the chart and use ← / → (Home / End) to inspect samples; Esc clears
// the cursor. The canvas is redrawn only when what it shows changes (new
// history, window, host, size or cursor), not on every snapshot.
import { el } from "./dom.js";
import { drawChart, drawCrosshair, indexAt } from "./chart.js";
import { formatRTT } from "./model.js";
import { formatTime } from "./time.js";
import { alignSamples, downsample, padTo } from "./timeline.js";
import { robustAxis } from "./scale.js";

export function createSummaryChart() {
  const windowSelect = el("select", { attrs: { id: "graph-window", "aria-label": "Chart time window" } });
  const title = el("h3", { text: "RTT" });
  const caption = el("span", { className: "chart-scale", text: "p95 scale · higher peaks are clipped (▲)" });
  const canvas = el("canvas", { className: "summary-canvas", attrs: { "aria-hidden": "true" } });
  const readout = el("p", { className: "graph-readout", attrs: { id: "summary-chart-readout" } });
  const empty = el("p", { className: "graph-empty", text: "Waiting for the first completed measurement…" });
  const announcement = el("span", { className: "visually-hidden", attrs: { role: "status", "aria-live": "polite" } });
  const frame = el("div", {
    className: "summary-chart",
    attrs: { tabindex: "0", role: "group", "aria-describedby": "summary-chart-readout" },
  }, canvas, empty);
  const node = el("section", { className: "inspect-section", attrs: { "data-section": "chart" } },
    el("div", { className: "chart-head" }, title, el("span", { className: "spacer" }), caption,
      el("label", { className: "tool" }, "Window", windowSelect)),
    frame, readout, announcement);

  let state = null; // {target, samples, meta, points, now}
  let cursor = -1;
  let reduced = { points: 0, size: 1, values: [], lost: [] };
  let geo = null;
  let lastKey = null;
  let queued = false;
  let announce = false;

  const paint = () => {
    queued = false;
    if (!state || !node.isConnected || canvas.clientWidth === 0) return;
    const { samples, meta, points, now } = state;
    const full = samples?.length && typeof samples[0] === "object" && samples[0] !== null
      ? alignSamples(samples, points, meta.interval_ms, now)
      : padTo(samples ?? [], points);
    // The scale comes from every sample in the window, so reducing to one
    // point per pixel (peak per bucket) cannot move it.
    const axis = robustAxis(full);
    const { values, lost, size } = downsample(full, Math.max(1, Math.floor(canvas.clientWidth)));
    reduced = { points: values.length, size, values, lost };
    if (cursor >= values.length) cursor = -1;
    geo = drawChart(canvas, values, meta.thresholds, meta.interval_ms, { axis, lost, samplesPerPoint: size });
    if (cursor >= 0) drawCrosshair(canvas, geo, values, cursor);
    empty.hidden = full.some((v) => v !== undefined);
    const v = cursor >= 0 ? values[cursor] : undefined;
    const at = formatTime(now - (values.length - 1 - Math.max(cursor, 0)) * size * meta.interval_ms);
    readout.textContent = cursor < 0
      ? `${axis.clipped ? `Peak ${formatRTT(axis.peak)} is above the scale. ` : ""}Focus the chart and use ← / → to inspect samples.`
      : `${at} · ${sampleText(v, size, lost[cursor])}`;
    if (announce) announcement.textContent = `${state.target.host}: ${readout.textContent}`;
    announce = false;
  };
  const schedule = () => {
    if (queued) return;
    queued = true;
    requestAnimationFrame(paint);
  };

  frame.addEventListener("keydown", (e) => {
    if (e.isComposing || e.ctrlKey || e.metaKey || e.altKey) return;
    if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) {
      e.preventDefault();
      e.stopPropagation();
      if (!reduced.points) return;
      cursor = movedCursor(e.key, cursor, reduced.points);
      announce = true;
      schedule();
    } else if (e.key === "Escape" && cursor >= 0) {
      e.preventDefault();
      e.stopPropagation();
      cursor = -1;
      schedule();
    }
  });
  canvas.addEventListener("pointermove", (e) => {
    if (!geo || reduced.points === 0) return;
    const next = indexAt(geo, reduced.points, e.offsetX);
    if (next !== cursor) {
      cursor = next;
      schedule();
    }
  });
  canvas.addEventListener("pointerleave", () => {
    if (document.activeElement === frame) return;
    cursor = -1;
    schedule();
  });
  new ResizeObserver(schedule).observe(frame);

  return {
    node,
    windowSelect,
    /**
     * @param {{target: object, samples: object[]|undefined, meta: object, points: number, now: number, windowLabel: string}} next
     */
    render(next) {
      if (state?.target.id !== next.target.id) cursor = -1;
      state = next;
      title.textContent = `RTT · ${next.windowLabel ?? ""}`.trim();
      frame.setAttribute("aria-label", `RTT chart for ${next.target.host}; use left and right arrow keys to inspect samples`);
      const key = [next.target.id, next.samples, next.points, next.now, next.meta.interval_ms, JSON.stringify(next.meta.thresholds)];
      if (!lastKey || key.some((v, i) => v !== lastKey[i])) {
        lastKey = key;
        schedule();
      }
    },
    /** Forget the drawn state, e.g. when Summary closes. */
    reset() {
      state = null;
      lastKey = null;
      cursor = -1;
    },
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
