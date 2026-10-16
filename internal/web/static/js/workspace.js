import { buildSections, lossRate, matchesFilter, rowLevel } from "./model.js";

export const SORTS = {
  config: "Configured order", status: "Status", host: "Host", loss: "Loss (lifetime)",
  last: "RTT", jitter: "Jitter", sent: "Sent", recv: "Recv", ip: "IP",
};
const severity = { crit: 4, warn: 3, pending: 2, ok: 1 };

// Read only the requested metric. Sorting by a name or count does not need
// health classification or allocation of an object containing every metric.
const sortValues = {
  status: (target, thresholds) => severity[rowLevel(target, thresholds)],
  host: (target) => target.host.toLowerCase(),
  loss: (target) => target.recv + target.loss ? lossRate(target) : null,
  last: (target) => target.last_rtt_ms,
  jitter: (target) => target.jitter_ms,
  sent: (target) => target.sent,
  recv: (target) => target.recv,
  ip: (target) => target.ip || null,
};

export function configuredTargets(targets, groups) {
  return buildSections(targets, groups).flatMap(section => section.targets);
}

export function sortTargets(targets, sort, thresholds) {
  if (!sort || sort.key === "config") return [...targets];
  const readValue = Object.hasOwn(sortValues, sort.key) ? sortValues[sort.key] : () => undefined;
  return targets.map((target, index) => ({ target, index, value: readValue(target, thresholds) })).sort((a, b) => {
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

/** Owns the frozen order; derives one shared list for every monitor pane. */
export function createTargetOrder() {
  let savedIds = null;
  const ids = (targets) => targets.map((target) => target.id);
  const baseTargets = (view) => configuredTargets(view.snapshot.targets, view.meta.groups);
  const rankedIds = (targets, view) => ids(sortTargets(targets, view.sort, view.meta.thresholds));

  return {
    refresh(view) {
      savedIds = rankedIds(baseTargets(view), view);
    },

    derive(view) {
      const base = baseTargets(view);
      const ranked = rankedIds(base, view);
      if (savedIds === null) savedIds = ranked;
      const ordered = view.sort.key === "config" ? base : frozenOrder(base, savedIds);
      return {
        targets: filterTargets(ordered, view.filter, view.status, view.meta),
        total: base.length,
        orderUpToDate: view.sort.key === "config" || ranked.every((id, index) => id === ordered[index]?.id),
      };
    },
  };
}
