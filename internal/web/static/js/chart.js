// Canvas RTT charts. One series (RTT) so no legend: the surrounding title
// names it. Lost probes (null samples) are drawn as critical ticks on the
// baseline and break the line; undefined samples mean "no data yet" (a
// target added after the window began) and are left blank.
import { cssVar } from "./dom.js";
import { formatMs } from "./model.js";
import { agoLabel } from "./timeline.js";

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

function seriesMax(series) {
  let max = 0;
  for (const v of series) if (v != null && v > max) max = v;
  return max;
}

/** Axis/threshold label: whole numbers stay whole ("50", not "50.0"). */
function tickLabel(v) {
  return Number.isInteger(v) ? String(v) : formatMs(v);
}

/** Rounds up to 1/2/5 × 10^k so axis ticks land on clean numbers. */
function niceCeil(v) {
  if (!(v > 0)) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 5, 10]) if (v <= m * p) return m * p;
  return 10 * p;
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

/** Per-row sparkline: shape only, scaled to its own maximum. */
export function drawSparkline(canvas, series) {
  const { ctx, w, h } = setupCanvas(canvas);
  if (!series || series.length === 0) return;
  const pad = 4;
  const max = seriesMax(series) || 1;
  const n = Math.max(series.length - 1, 1);
  const xAt = (i) => pad + (i / n) * (w - pad * 2);
  const yAt = (v) => h - pad - (v / max) * (h - pad * 2);
  const colour = cssVar("--series");
  plotLine(ctx, series, xAt, yAt, h - pad, colour);
  plotLosses(ctx, series, xAt, h, 4);
  const last = series.length - 1;
  if (series[last] != null) endDot(ctx, xAt(last), yAt(series[last]), colour);
}

/**
 * RTT chart with a y axis in ms, warn/crit reference lines, and an
 * "s ago" x axis derived from the probe interval. Returns the geometry the
 * hover layer needs.
 */
export function drawChart(canvas, series, th, intervalMs, opts = {}) {
  const { ctx, w, h } = setupCanvas(canvas);
  const left = 44, right = 12, top = 10, bottom = 24;
  const plotW = w - left - right;
  const plotH = h - top - bottom;
  // opts.yMax shares one scale across several charts (e.g. all targets);
  // opts.lost flags lost probes when `series` was downsampled.
  const max = niceCeil(Math.max((opts.yMax ?? seriesMax(series)) * 1.15, 1));
  const n = Math.max(series.length - 1, 1);
  const xAt = (i) => left + (i / n) * plotW;
  const yAt = (v) => top + plotH - (Math.min(v, max) / max) * plotH;
  const baseY = top + plotH;

  ctx.font = `11px ${cssVar("--font")}`;
  ctx.textBaseline = "middle";
  ctx.lineWidth = 1;
  for (let k = 0; k <= 4; k++) {
    const v = (max / 4) * k;
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
