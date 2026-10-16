import { buildSections, lossRate, matchesFilter, rowLevel } from "./model.js";

export const SORTS = {
  config: "Configured order", status: "Status", host: "Host", loss: "Loss (lifetime)",
  last: "RTT", jitter: "Jitter", sent: "Sent", recv: "Recv", ip: "IP",
};
const severity = { crit: 4, warn: 3, pending: 2, ok: 1 };

export function configuredTargets(targets, groups) {
  return buildSections(targets, groups).flatMap(section => section.targets);
}

export function sortTargets(targets, sort, thresholds) {
  if (!sort || sort.key === "config") return [...targets];
  const value = t => ({ status: severity[rowLevel(t, thresholds)], host: t.host.toLowerCase(),
    loss: t.recv + t.loss ? lossRate(t) : null, last: t.last_rtt_ms, jitter: t.jitter_ms,
    sent: t.sent, recv: t.recv, ip: t.ip || null })[sort.key];
  return targets.map((target, index) => ({ target, index, value: value(target) })).sort((a, b) => {
    if (a.value == null || b.value == null) return a.value == null && b.value == null ? a.index - b.index : a.value == null ? 1 : -1;
    const cmp = typeof a.value === "string" ? a.value.localeCompare(b.value, undefined, { numeric: true }) : a.value - b.value;
    return (sort.direction === "asc" ? cmp : -cmp) || a.index - b.index;
  }).map(item => item.target);
}

// Store identities, not values: incoming measurements cannot move existing rows.
export function frozenOrder(targets, ids = []) {
  const byId = new Map(targets.map(t => [t.id, t]));
  const ordered = ids.filter(id => byId.has(id)).map(id => byId.get(id));
  const known = new Set(ids);
  return [...ordered, ...targets.filter(t => !known.has(t.id))];
}

export function filterTargets(targets, query, status, meta) {
  return targets.filter(t => matchesFilter(t, query, meta.features) &&
    (!status || status === "all" || rowLevel(t, meta.thresholds) === status));
}

export function editableTarget(target) {
  return target instanceof Element && !!target.closest("input, textarea, select, [contenteditable]:not([contenteditable=false])");
}
