// Pure logic for the Activity panel (no DOM, unit-tested under node): how
// long each host has been in its state, which hosts to list and in what
// order, a summary of the last hour of health events, and the recent events
// across all hosts.
import { rowLevel, statusReason } from "./model.js";
import { parseBreaches, shortReason, withUnit } from "./health_text.js";

export const HOUR_MS = 3600 * 1000;
/** Events the server keeps per target (internal/stats maxTargetEvents). */
export const PER_TARGET_EVENT_CAP = 128;
/** Transitions within this span count as simultaneous. */
export const SIMULTANEOUS_MS = 5000;
export const SIMULTANEOUS_MIN_HOSTS = 3;

const SEVERITY = { crit: 3, warn: 2, pending: 1, ok: 0 };

/** "42s", "3m 12s", "18m", "1h 5m", "2d 3h"; "–" when unknown. */
export function formatDuration(ms) {
  if (!Number.isFinite(ms) || ms < 0) return "–";
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 10) return s % 60 ? `${m}m ${s % 60}s` : `${m}m`;
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 24) return m % 60 ? `${h}h ${m % 60}m` : `${h}h`;
  const d = Math.floor(h / 24);
  return h % 24 ? `${d}d ${h % 24}h` : `${d}d`;
}

/** "Jitter 12.3 ms (threshold 10.0 ms)"; other reasons as the server wrote them. */
export function reasonWithThreshold(reason) {
  const breaches = parseBreaches(reason);
  if (breaches.length === 0) return reason ?? "";
  return breaches.map((b) => `${b.metric} ${withUnit(b.value, b.unit)} (threshold ${withUnit(b.threshold, b.unit)})`).join(" · ");
}

/**
 * Hosts to list with their current state and how long it has lasted.
 * mode "all" lists every host, problems first (severity, then longest).
 * mode "attention" lists only non-OK hosts, longest-lasting first, and
 * counts the OK ones.
 * @returns {{items: {target: object, level: string, reason: string, durationMs: number|null}[], okCount: number}}
 */
export function activityItems(targets, { mode = "all", now = Date.now() } = {}) {
  const rows = (targets ?? []).map((target, index) => {
    const level = rowLevel(target);
    const since = Date.parse(target.health?.since ?? "");
    return {
      target, index, level,
      reason: level === "ok" ? "" : reasonWithThreshold(statusReason(target)),
      durationMs: Number.isFinite(since) ? Math.max(0, now - since) : null,
    };
  });
  const longest = (a, b) => (b.durationMs ?? -1) - (a.durationMs ?? -1);
  const okCount = rows.filter((r) => r.level === "ok").length;
  const listed = mode === "attention"
    ? rows.filter((r) => r.level !== "ok").sort((a, b) => longest(a, b) || SEVERITY[b.level] - SEVERITY[a.level] || a.index - b.index)
    : [...rows].sort((a, b) => SEVERITY[b.level] - SEVERITY[a.level] || longest(a, b) || a.index - b.index);
  return { items: listed.map(({ index, ...item }) => item), okCount };
}

const HEALTH = /^(ok|warn|crit|pending): (.*)$/;

/** A health transition event as {level, reason}, or null for other kinds. */
export function healthTransition(event) {
  if (event?.kind !== "health") return null;
  const m = HEALTH.exec(event.message ?? "");
  return m ? { level: m[1], reason: m[2] } : null;
}

/**
 * What the last hour of events can honestly say.
 * - changes: health transitions, split by the state entered.
 * - leastStable: the host with the most transitions (at least 2).
 * - simultaneous: moments when at least SIMULTANEOUS_MIN_HOSTS hosts started
 *   breaching the same metric within SIMULTANEOUS_MS.
 * - partial: the event list may not reach back a full hour (the request
 *   limit was hit, or a host filled its retained-event ring), so counts are
 *   lower bounds.
 * @param {object[]} events all-hosts events ({at, host, kind, message, ...})
 */
export function lastHourSummary(events, { now = Date.now(), limit = Infinity } = {}) {
  const all = events ?? [];
  const from = now - HOUR_MS;
  const perHost = new Map();
  for (const e of all) perHost.set(e.host, (perHost.get(e.host) ?? 0) + 1);
  const oldest = Math.min(...all.map((e) => Date.parse(e.at)).filter(Number.isFinite));
  const partial = (all.length >= limit || [...perHost.values()].some((n) => n >= PER_TARGET_EVENT_CAP))
    && oldest > from;

  const transitions = all
    .map((e) => ({ host: e.host, at: Date.parse(e.at), health: healthTransition(e) }))
    .filter((e) => e.health && e.at >= from && e.at <= now)
    .sort((a, b) => a.at - b.at);
  const changes = { total: transitions.length, warn: 0, crit: 0, ok: 0, pending: 0 };
  const byHost = new Map();
  for (const t of transitions) {
    changes[t.health.level]++;
    byHost.set(t.host, (byHost.get(t.host) ?? 0) + 1);
  }
  let leastStable = null;
  for (const [host, count] of byHost) {
    if (count >= 2 && (!leastStable || count > leastStable.count)) leastStable = { host, count };
  }
  return { changes, leastStable, simultaneous: simultaneousBreaches(transitions), partial };
}

/** Clusters of hosts starting to breach the same metric at about the same time. */
function simultaneousBreaches(transitions) {
  const byMetric = new Map();
  for (const t of transitions) {
    if (t.health.level !== "warn" && t.health.level !== "crit") continue;
    for (const b of parseBreaches(t.health.reason)) {
      const list = byMetric.get(b.metric) ?? [];
      list.push(t);
      byMetric.set(b.metric, list);
    }
  }
  const out = [];
  for (const [metric, list] of byMetric) {
    let i = 0;
    while (i < list.length) {
      const start = list[i].at;
      const hosts = new Set();
      let j = i;
      while (j < list.length && list[j].at - start <= SIMULTANEOUS_MS) hosts.add(list[j++].host);
      if (hosts.size >= SIMULTANEOUS_MIN_HOSTS) {
        out.push({ metric, hosts: hosts.size, at: new Date(start).toISOString() });
        i = j;
      } else {
        i++;
      }
    }
  }
  return out.sort((a, b) => Date.parse(b.at) - Date.parse(a.at));
}

/**
 * The newest events across all hosts, with identical events that happened
 * within a second on several hosts folded into one line ("3 hosts").
 * @returns {{at: string, level: string|null, kind: string, text: string, hosts: string[]}[]}
 */
export function recentAcrossHosts(events, limit = 6) {
  const time = (e) => Date.parse(e.last_at ?? e.at);
  const sorted = [...(events ?? [])].sort((a, b) => time(b) - time(a));
  const out = [];
  for (const e of sorted) {
    const health = healthTransition(e);
    const level = health?.level ?? null;
    const text = health ? shortReason(health.reason) : (e.count > 1 ? `${e.message} ×${e.count}` : e.message);
    const last = out.at(-1);
    if (last && last.level === level && last.kind === e.kind && last.text === text
        && Math.abs(Date.parse(last.at) - time(e)) <= 1000 && !last.hosts.includes(e.host)) {
      last.hosts.push(e.host);
      continue;
    }
    if (out.length === limit) break;
    out.push({ at: new Date(time(e)).toISOString(), level, kind: e.kind, text, hosts: [e.host] });
  }
  return out;
}
