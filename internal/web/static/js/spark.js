// Monitor-row sparkline geometry (no DOM, unit-tested under node). The
// series is the last minute of wall-time-aligned samples: numbers are RTTs,
// null a lost probe and undefined an interval with no completed probe.
import { downsample } from "./timeline.js";

export const SPARK_SECONDS = 60;
/** More points than this are reduced (peak per bucket) before drawing. */
export const SPARK_MAX_POINTS = 120;

const fixed = (n) => Number(n.toFixed(1));

/**
 * @param {(number|null|undefined)[]} series
 * @param {{width: number, height: number, max: number, seconds?: number}} box
 * @returns {{
 *   runs: string[], losses: number[], clipped: number[],
 *   allLost: boolean, empty: boolean, lostCount: number,
 *   min: number|null, avg: number|null, max: number|null, label: string,
 * }} runs are SVG "x,y x,y" point lists broken at gaps; losses and clipped
 *    are x positions of lost and above-the-top samples.
 */
export function sparkGeometry(series, { width, height, max, seconds = SPARK_SECONDS }) {
  const source = series ?? [];
  const { values, lost } = source.length > SPARK_MAX_POINTS
    ? downsample(source, SPARK_MAX_POINTS)
    : { values: source, lost: source.map((v) => v === null) };
  const n = values.length;
  const top = max > 0 ? max : 1;
  const xAt = (i) => fixed(n <= 1 ? width : (i / (n - 1)) * width);
  // 1px margins keep a 1.5px stroke inside the box at both extremes.
  const yAt = (v) => fixed(height - 1 - (Math.min(v, top) / top) * (height - 2));

  const runs = [];
  const losses = [];
  const clipped = [];
  let run = [];
  // Use every sample for the label; drawing may keep only peaks per bucket.
  let lo = Infinity, hi = -Infinity, sum = 0, replies = 0, lostCount = 0;
  for (const v of source) {
    if (v === null) lostCount++;
    if (!Number.isFinite(v)) continue;
    lo = Math.min(lo, v);
    hi = Math.max(hi, v);
    sum += v;
    replies++;
  }
  const flush = () => {
    // A lone sample would draw nothing; give it a 1px stub.
    if (run.length === 1) run.push([fixed(Math.min(width, run[0][0] + 1)), run[0][1]]);
    if (run.length) runs.push(run.map(([x, y]) => `${x},${y}`).join(" "));
    run = [];
  };
  values.forEach((v, i) => {
    if (lost[i]) losses.push(xAt(i));
    if (!Number.isFinite(v)) {
      flush();
      return;
    }
    if (v > top) clipped.push(xAt(i));
    run.push([xAt(i), yAt(v)]);
  });
  flush();

  const empty = replies === 0 && lostCount === 0;
  const allLost = replies === 0 && lostCount > 0;
  const min = replies ? lo : null, avg = replies ? sum / replies : null, peak = replies ? hi : null;
  return { runs, losses, clipped, allLost, empty, lostCount, min, avg, max: peak,
    label: sparkLabel({ empty, allLost, min, avg, max: peak, lostCount, seconds }) };
}

const ms = (v) => (v >= 10 ? String(Math.round(v)) : v >= 1 ? v.toFixed(1) : v.toFixed(2));

/** Accessible name for a sparkline. */
export function sparkLabel({ empty, allLost, min, avg, max, lostCount, seconds = SPARK_SECONDS }) {
  if (empty) return `No measurements in the last ${seconds} seconds`;
  if (allLost) return `No replies in the last ${seconds} seconds; ${lostCount} lost`;
  return `RTT last ${seconds} s: min ${ms(min)} ms, avg ${ms(avg)} ms, max ${ms(max)} ms; ${lostCount} lost`;
}
