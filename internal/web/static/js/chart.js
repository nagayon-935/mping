// Canvas RTT charts. One series (RTT) so no legend: the surrounding title
// names it. Lost probes (null samples) are drawn as critical ticks on the
// baseline and break the line; undefined samples mean "no data yet" (a
// target added after the window began) and are left blank.
import { cssVar } from "./dom.js";
import { formatMs } from "./model.js";
import { agoLabel, sharedMax } from "./timeline.js";
import { clippedPoints, rttAxis } from "./scale.js";

export { rttAxis };

function setupCanvas(canvas) {
  const dpr = window.devicePixelRatio || 1;
  const w = canvas.clientWidth;
  const h = canvas.clientHeight;
  if (canvas.width !== Math.round(w * dpr) || canvas.height !== Math.round(h * dpr)) {
    canvas.width = Math.round(w * dpr);
    canvas.height = Math.round(h * dpr);
  }
  const ctx = canvas.getContext("2d");
  ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  ctx.clearRect(0, 0, w, h);
  return { ctx, w, h };
}

/** Axis/threshold label: whole numbers stay whole ("50", not "50.0"). */
function tickLabel(v) {
  return Number.isInteger(v) ? String(v) : formatMs(v);
}

/** Strokes the series as line segments, breaking at nulls; fills a wash under each run. */
function plotLine(ctx, series, xAt, yAt, baseY, colour) {
  let run = [];
  const flush = () => {
    if (run.length === 0) return;
    ctx.beginPath();
    ctx.moveTo(run[0][0], baseY);
    for (const [x, y] of run) ctx.lineTo(x, y);
    ctx.lineTo(run[run.length - 1][0], baseY);
    ctx.closePath();
    ctx.globalAlpha = 0.1;
    ctx.fillStyle = colour;
    ctx.fill();
    ctx.globalAlpha = 1;
    ctx.beginPath();
    run.forEach(([x, y], i) => (i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y)));
    if (run.length === 1) ctx.lineTo(run[0][0] + 0.5, run[0][1]);
    ctx.strokeStyle = colour;
    ctx.lineWidth = 2;
    ctx.lineJoin = "round";
    ctx.lineCap = "round";
    ctx.stroke();
    run = [];
  };
  series.forEach((v, i) => {
    if (v == null) flush();
    else run.push([xAt(i), yAt(v)]);
  });
  flush();
}

/** Critical ticks for lost probes; `lost` (when given) flags them per point. */
function plotLosses(ctx, series, xAt, baseY, tickH, lost) {
  ctx.fillStyle = cssVar("--critical");
  series.forEach((v, i) => {
    if (lost ? lost[i] : v === null) ctx.fillRect(xAt(i) - 1, baseY - tickH, 2, tickH);
  });
}

/**
 * Samples above the axis top are drawn clipped to it; a small triangle marks
 * each one and the highest is labelled ("▲ 680 ms").
 */
function plotClipped(ctx, series, xAt, top, max) {
  const { indexes, peak } = clippedPoints(series, max);
  if (!peak) return;
  ctx.fillStyle = cssVar("--critical-ink");
  for (const i of indexes) {
    const x = xAt(i);
    ctx.beginPath();
    ctx.moveTo(x, top - 1);
    ctx.lineTo(x - 4, top + 6);
    ctx.lineTo(x + 4, top + 6);
    ctx.closePath();
    ctx.fill();
  }
  const x = xAt(peak.index);
  const text = `▲ ${formatMs(peak.value)} ms`;
  ctx.font = `600 11px ${cssVar("--font")}`;
  const width = ctx.measureText(text).width;
  const right = ctx.canvas.clientWidth - 12;
  ctx.textBaseline = "bottom";
  ctx.textAlign = "left";
  ctx.fillText(text, Math.max(0, Math.min(x - width / 2, right - width)), top - 3);
  ctx.font = `11px ${cssVar("--font")}`;
}

function endDot(ctx, x, y, colour) {
  ctx.beginPath();
  ctx.arc(x, y, 4, 0, Math.PI * 2);
  ctx.fillStyle = cssVar("--surface");
  ctx.fill();
  ctx.beginPath();
  ctx.arc(x, y, 3, 0, Math.PI * 2);
  ctx.fillStyle = colour;
  ctx.fill();
}

/**
 * RTT chart with a y axis in ms, warn/crit reference lines, and an
 * "s ago" x axis derived from the probe interval. Returns the geometry the
 * hover layer needs.
 */
export function drawChart(canvas, series, th, intervalMs, opts = {}) {
  const { ctx, w, h } = setupCanvas(canvas);
  // opts.axis (e.g. a robust p95 scale) wins over opts.yMax / the peak.
  const { max, ticks } = opts.axis ?? rttAxis(opts.yMax ?? sharedMax([series]));
  ctx.font = `11px ${cssVar("--font")}`;
  const left = Math.max(44, ...ticks.map(v => ctx.measureText(tickLabel(v)).width + 12));
  const right = 12, top = 20, bottom = 24;
  const plotW = w - left - right;
  const plotH = h - top - bottom;
  // opts.yMax shares one scale across several charts (e.g. all targets);
  // opts.lost flags lost probes when `series` was downsampled.
  const n = Math.max(series.length - 1, 1);
  const xAt = (i) => left + (i / n) * plotW;
  const yAt = (v) => top + plotH - (Math.min(v, max) / max) * plotH;
  const baseY = top + plotH;

  ctx.textBaseline = "middle";
  ctx.lineWidth = 1;
  ctx.fillStyle = cssVar("--muted");
  ctx.textAlign = "right";
  ctx.fillText("ms", left - 6, 7);
  for (const [k, v] of ticks.entries()) {
    const y = Math.round(yAt(v)) + 0.5;
    ctx.strokeStyle = k === 0 ? cssVar("--axis") : cssVar("--grid");
    ctx.beginPath();
    ctx.moveTo(left, y);
    ctx.lineTo(left + plotW, y);
    ctx.stroke();
    ctx.fillStyle = cssVar("--muted");
    ctx.textAlign = "right";
    ctx.fillText(tickLabel(v), left - 6, y);
  }

  for (const [value, token, label] of [
    [th.rtt_warn_ms, "--warning", "warn"],
    [th.rtt_crit_ms, "--critical", "crit"],
  ]) {
    if (!(value > 0) || value > max) continue;
    const y = Math.round(yAt(value)) + 0.5;
    ctx.strokeStyle = cssVar(token);
    ctx.beginPath();
    ctx.moveTo(left, y);
    ctx.lineTo(left + plotW, y);
    ctx.stroke();
    ctx.fillStyle = cssVar("--muted");
    ctx.textAlign = "left";
    // Above the line, unless that would clip at the top of the plot.
    const labelY = y - 7 < top + 6 ? y + 8 : y - 7;
    ctx.fillText(`${label} ${tickLabel(value)} ms`, left + 4, labelY);
  }

  if (series.length > 0) {
    ctx.fillStyle = cssVar("--muted");
    ctx.textBaseline = "top";
    ctx.textAlign = "left";
    // The left edge is where the window starts: a full window of samples ago.
    ctx.fillText(agoLabel(series.length * (opts.samplesPerPoint ?? 1), intervalMs), left, baseY + 6);
    ctx.textAlign = "right";
    ctx.fillText("now", left + plotW, baseY + 6);

    const colour = cssVar("--series");
    plotLine(ctx, series, xAt, yAt, baseY, colour);
    plotLosses(ctx, series, xAt, baseY, 6, opts.lost);
    plotClipped(ctx, series, xAt, top, max);
  }
  return { left, plotW, top, plotH, xAt, yAt, n, max };
}

/** Crosshair at index i, drawn over an existing chart. */
export function drawCrosshair(canvas, geo, series, i) {
  const ctx = canvas.getContext("2d");
  const x = Math.round(geo.xAt(i)) + 0.5;
  ctx.strokeStyle = cssVar("--muted");
  ctx.lineWidth = 1;
  ctx.beginPath();
  ctx.moveTo(x, geo.top);
  ctx.lineTo(x, geo.top + geo.plotH);
  ctx.stroke();
  if (series[i] != null) endDot(ctx, geo.xAt(i), geo.yAt(series[i]), cssVar("--series"));
}

/** Nearest sample index for a pointer x in CSS pixels. */
export function indexAt(geo, length, x) {
  if (length === 0) return -1;
  const t = (x - geo.left) / geo.plotW;
  return Math.max(0, Math.min(length - 1, Math.round(t * (length - 1))));
}
