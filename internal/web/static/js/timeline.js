// Time-window logic for the RTT graphs pane (no DOM, unit-tested under node).
//
// mping probes every target on one shared interval and keeps each target's
// RTT samples in a ring, newest last. Counting samples back from the newest
// one therefore lines every target up on the same time axis, including
// hosts added later (their series are simply shorter).

const CANDIDATES = [[60, "1m"], [300, "5m"], [900, "15m"], [1800, "30m"], [3600, "1h"]];

/**
 * Time windows whose samples fit in the history ring.
 * @returns {{label: string, seconds: number, points: number}[]}
 */
export function windowOptions(intervalMs, maxPoints) {
  const opts = [];
  for (const [seconds, label] of CANDIDATES) {
    const points = Math.ceil((seconds * 1000) / intervalMs);
    if (points <= maxPoints) opts.push({ label, seconds, points });
  }
  if (opts.length > 0) return opts;
  // Very short intervals: offer the whole ring instead.
  const seconds = Math.round((maxPoints * intervalMs) / 1000);
  const label = seconds < 60 ? `${seconds}s` : `${Math.floor(seconds / 60)}m`;
  return [{ label, seconds, points: maxPoints }];
}

/** 5 minutes when available, otherwise the widest window offered. */
export function defaultWindow(options) {
  return options.find((o) => o.label === "5m") ?? options[options.length - 1];
}

/** Largest RTT across all series (lost probes are null), for a shared y axis. */
export function sharedMax(seriesList) {
  let max = 0;
  for (const series of seriesList) {
    for (const v of series) if (v != null && v > max) max = v;
  }
  return max;
}

/** "now", "12s ago", "1m 20s ago" for a cursor `samplesAgo` from the newest. */
export function agoLabel(samplesAgo, intervalMs) {
  const total = Math.round((samplesAgo * intervalMs) / 1000);
  if (samplesAgo === 0) return "now";
  if (total === 0) return "<1s ago";
  const m = Math.floor(total / 60);
  const s = total % 60;
  if (m === 0) return `${s}s ago`;
  return s === 0 ? `${m}m ago` : `${m}m ${s}s ago`;
}

/**
 * The newest `points` samples, left-padded with undefined ("no data yet")
 * so every target's series spans the same window and shares cursor indexes.
 * Lost probes stay null.
 */
export function padTo(series, points) {
  const recent = tail(series, points);
  if (recent.length === points) return recent;
  return [...new Array(points - recent.length).fill(undefined), ...recent];
}

/** The newest `n` samples of a series. */
export function tail(series, n) {
  if (!series) return [];
  return series.length <= n ? series : series.slice(series.length - n);
}
