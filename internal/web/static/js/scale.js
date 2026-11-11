// RTT axis scales (no DOM, unit-tested under node).
//
// A robust scale sets the top from the 95th percentile instead of the peak,
// so one 680 ms outlier does not flatten five minutes of 15 ms samples. The
// samples above the top are drawn clipped, with a marker naming the peak.

/** Smallest axis top a robust scale uses, so sub-millisecond noise stays flat. */
export const ROBUST_MIN_MS = 1;

/** Rounds up to 1/2/5 × 10^k so axis ticks land on clean numbers. */
function niceCeil(v) {
  if (!(v > 0)) return 1;
  const p = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 5, 10]) if (v <= m * p) return m * p;
  return 10 * p;
}

/** Four equal intervals, rounding the interval itself to a readable value. */
export function rttAxis(peak) {
  const step = niceCeil(Math.max(Number.isFinite(peak) ? peak * 1.15 : 0, 1) / 4);
  const ticks = Array.from({ length: 5 }, (_, i) => Number((step * i).toPrecision(12)));
  return { max: ticks[4], ticks };
}

/** Finite RTT samples; lost (null) and missing (undefined) ones are skipped. */
export function finiteValues(series) {
  const out = [];
  for (const v of series ?? []) if (typeof v === "number" && Number.isFinite(v)) out.push(v);
  return out;
}

/** Nearest-rank percentile (p in 0..1) of the finite samples; 0 when there are none. */
export function percentile(series, p) {
  const values = finiteValues(series).sort((a, b) => a - b);
  if (values.length === 0) return 0;
  const rank = Math.ceil(Math.min(Math.max(p, 0), 1) * values.length);
  return values[Math.max(0, rank - 1)];
}

/**
 * Axis whose top is a rounded value just above the p95 (never below
 * `minimum`). `clipped` says whether any sample lies above the top.
 * @returns {{max: number, ticks: number[], p95: number, peak: number, clipped: boolean}}
 */
export function robustAxis(series, { minimum = ROBUST_MIN_MS } = {}) {
  const values = finiteValues(series);
  const p95 = percentile(values, 0.95);
  const peak = values.length ? Math.max(...values) : 0;
  const axis = rttAxis(Math.max(p95, minimum));
  return { ...axis, p95, peak, clipped: peak > axis.max };
}

/**
 * Indexes of samples above `max`, and the highest of them, for the
 * "▲ 680 ms" marker. peak is null when nothing is clipped.
 * @returns {{indexes: number[], peak: {index: number, value: number} | null}}
 */
export function clippedPoints(series, max) {
  const indexes = [];
  let peak = null;
  (series ?? []).forEach((v, i) => {
    if (typeof v !== "number" || !(v > max)) return;
    indexes.push(i);
    if (!peak || v > peak.value) peak = { index: i, value: v };
  });
  return { indexes, peak };
}
