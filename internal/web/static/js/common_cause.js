// Common-cause detection (no DOM, unit-tested under node). When most of the
// hosts that are answering breach the same threshold at the same moment, the
// cause is more likely on this side (local link, Wi-Fi, upstream) than at
// each host, so the monitor states that once instead of in every row.
import { parseBreaches, withUnit } from "./health_text.js";

const COMMON_CAUSE_MIN_TARGETS = 3;
const COMMON_CAUSE_RATIO = 0.75;

/** Replying to probes now: judged (not waiting) and not marked down. */
function answering(t) {
  const status = t.health?.status;
  return (status === "ok" || status === "warn" || status === "crit") && !t.health?.unavailable;
}

/**
 * The shared breach, or null. At least `minTargets` hosts must be answering
 * and at least `ratio` of them must breach the same metric. When several
 * metrics qualify, the one breached by the most hosts wins (ties keep the
 * server's order: Loss, RTT, Jitter).
 * @returns {null | {metric: string, unit: string, count: number, answering: number, total: number, min: number, max: number}}
 */
export function detectCommonCause(targets, { minTargets = COMMON_CAUSE_MIN_TARGETS, ratio = COMMON_CAUSE_RATIO } = {}) {
  if (!Array.isArray(targets)) return null;
  const replying = targets.filter(answering);
  if (replying.length < minTargets) return null;
  const byMetric = new Map();
  for (const t of replying) {
    if (t.health.status === "ok") continue;
    const seen = new Set();
    for (const breach of parseBreaches(t.health.reason)) {
      if (seen.has(breach.metric)) continue;
      seen.add(breach.metric);
      const entry = byMetric.get(breach.metric) ?? { metric: breach.metric, unit: breach.unit, values: [] };
      entry.values.push(breach.value);
      byMetric.set(breach.metric, entry);
    }
  }
  let best = null;
  for (const entry of byMetric.values()) {
    if (entry.values.length < ratio * replying.length) continue;
    if (!best || entry.values.length > best.values.length) best = entry;
  }
  if (!best) return null;
  return {
    metric: best.metric,
    unit: best.unit,
    count: best.values.length,
    answering: replying.length,
    total: targets.length,
    min: Math.min(...best.values),
    max: Math.max(...best.values),
  };
}

/** Whole numbers from 10 up ("47–51 ms"); smaller values keep a decimal. */
function rangeValue(v) {
  return v >= 10 ? String(Math.round(v)) : v.toFixed(1);
}

/** "47–51 ms", "48 ms", "25–40%". */
export function formatRange(min, max, unit) {
  const lo = rangeValue(min), hi = rangeValue(max);
  const span = lo === hi ? lo : `${lo}–${hi}`;
  return unit === "%" ? `${span}%` : `${span} ms`;
}

/** Banner text: a headline and a short interpretation. */
export function commonCauseText(cause) {
  return {
    headline: `Common cause: ${cause.metric} over threshold on ${cause.count} / ${cause.total} hosts (${formatRange(cause.min, cause.max, cause.unit)})`,
    detail: "Likely the local link or upstream rather than the individual hosts.",
    title: `${cause.count} of ${cause.answering} answering hosts breach the ${cause.metric} threshold at the same time `
      + `(values ${withUnit(cause.min, cause.unit)} to ${withUnit(cause.max, cause.unit)}).`,
  };
}
