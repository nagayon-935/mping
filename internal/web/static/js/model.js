// Pure dashboard logic: no DOM, so it can be unit-tested under node.
// Levels (ok/warn/crit/...) are decided by the server (internal/stats) and
// arrive with each snapshot; nothing here compares values with thresholds.

/** Loss rate in percent over completed probes, excluding in-flight ones. */
export function lossRate(t) {
  const done = t.recv + t.loss;
  return done === 0 ? 0 : (t.loss / done) * 100;
}

const rowLevels = new Set(["pending", "ok", "warn", "crit"]);

/** Overall status of a target row: the server's current health judgment. */
export function rowLevel(t) {
  return rowLevels.has(t.health?.status) ? t.health.status : "pending";
}

/** Why the target has its rowLevel, as the server explained it. */
export function statusReason(t) {
  return t.health?.reason || "Waiting for a completed probe";
}

/** Per-cell levels for the colour-coded columns. */
export function cellLevels(t) {
  return { loss: t.levels?.loss ?? "none", last: t.levels?.last_rtt ?? "none", jitter: t.levels?.jitter ?? "none" };
}

/** Milliseconds with precision chosen by magnitude; "–" for no sample. */
export function formatMs(ms) {
  if (ms == null || !(ms > 0)) return "–";
  if (ms < 1) return ms.toFixed(3);
  if (ms < 10) return ms.toFixed(2);
  if (ms < 100) return ms.toFixed(1);
  return Math.round(ms).toString();
}

/** RTT with its unit; missing measurements remain a plain dash. */
export function formatRTT(ms, compact = true) {
  const value = formatMs(ms);
  if (value === "–") return value;
  // Go durations top out at ~9.22e12 ms: 10 characters including " ms".
  return `${compact && ms >= 1e6 ? ms.toExponential(2).replace("e+", "e") : value} ms`;
}

/** At most 8 characters over the nonnegative int64 counter range. */
export function formatCount(n) {
  if (n < 1e6) return n.toLocaleString("en-US");
  const units = [[1e6, "M"], [1e9, "B"], [1e12, "T"]];
  let i = units.findLastIndex(([scale]) => n >= scale);
  if (i < 0 || n >= 1e15) return n.toExponential(2).replace("e+", "e");
  for (;;) {
    // Work in tenths so rounding is decided on integers, then carry to the
    // next unit when rounding reaches 1000 (999.95M is 1B, not "1000M").
    const tenths = Math.round(n / (units[i][0] / 10));
    if (tenths < 10000) {
      const whole = tenths % 10 === 0 ? String(tenths / 10) : (tenths / 10).toFixed(1);
      return `${whole}${units[i][1]}`;
    }
    if (++i === units.length) return n.toExponential(2).replace("e+", "e");
  }
}

export function formatPct(pct) {
  return pct.toFixed(1);
}

/**
 * Orders targets into display sections. Without groups there is one unnamed
 * section; with groups, each group lists its live targets in group order and
 * any target in no group lands in a trailing "Ungrouped" section. Group IDs
 * that no longer match a live target are skipped (hosts can be deleted
 * between the target list and the group list being read).
 */
export function buildSections(targets, groups) {
  if (!groups || groups.length === 0) return [{ name: null, targets }];
  const byId = new Map(targets.map((t) => [t.id, t]));
  const placed = new Set();
  const sections = [];
  for (const g of groups) {
    const members = [];
    for (const id of g.target_ids) {
      const t = byId.get(id);
      if (t && !placed.has(id)) {
        members.push(t);
        placed.add(id);
      }
    }
    if (members.length > 0) sections.push({ name: g.name, targets: members });
  }
  const rest = targets.filter((t) => !placed.has(t.id));
  if (rest.length > 0) sections.push({ name: "Ungrouped", targets: rest });
  return sections;
}

export function summarize(targets) {
  const out = { total: targets.length, ok: 0, warn: 0, crit: 0, pending: 0 };
  for (const t of targets) out[rowLevel(t)]++;
  return out;
}

export function matchesFilter(t, query, features = {ptr: true, asn: true}) {
  const q = query.trim().toLowerCase();
  if (q === "") return true;
  return [t.host, t.ip, features.ptr && t.ptr, features.asn && t.asn, features.asn && t.org]
    .some((v) => typeof v === "string" && v.toLowerCase().includes(q));
}

/**
 * Header badge for the server-reported state and the stream connection.
 * A final "stopped" survives the disconnect that follows it.
 */
export function badgeFor(state, connected) {
  if (state === "stopped") return { level: "stopped", label: "Session ended" };
  if (!connected) {
    return state == null
      ? { level: "pending", label: "Connecting" }
      : { level: "warn", label: "Reconnecting" };
  }
  switch (state) {
    case "running": return { level: "ok", label: "Online" };
    case "reloading": return { level: "warn", label: "Reloading" };
    default: return { level: "pending", label: "Starting" };
  }
}

/** The control token from a "#token=<hex>" fragment, or null. */
export function tokenFromHash(hash) {
  const m = /^#token=([^&]+)$/.exec(hash ?? "");
  return m ? m[1] : null;
}

const MAX_HOST_LEN = 253;

/**
 * Shape checks matching the server's validHost, so most mistakes are caught
 * before a request. Duplicates and resolvability are left to mping.
 * @returns {{host: string} | {error: string}}
 */
export function validateHost(raw) {
  const host = (raw ?? "").trim();
  if (host === "") return { error: "Enter a host name or IP address." };
  if (host.length > MAX_HOST_LEN) return { error: `Host is longer than ${MAX_HOST_LEN} characters.` };
  if (/[\s\p{Cc}]/u.test(host)) return { error: "Host must not contain spaces or control characters." };
  return { host };
}

/**
 * Chooses the columns that fit in `available` pixels. Required columns are
 * always kept; the rest are added strictly in priority order (lower number
 * first) and the first one that does not fit ends the search, so a narrow,
 * low-priority column never takes the place of a more important one that was
 * dropped. The result keeps the caller's display order.
 */
export function selectColumns(columns, widthOf, available) {
  const chosen = new Set(columns.filter((c) => c.required));
  let used = 0;
  for (const c of chosen) used += widthOf(c);
  const rest = columns.filter((c) => !c.required).sort((a, b) => a.priority - b.priority);
  for (const c of rest) {
    const width = widthOf(c);
    if (used + width > available) break;
    chosen.add(c);
    used += width;
  }
  return columns.filter((c) => chosen.has(c));
}

/** True when `host` is an IPv4 or IPv6 literal (including scoped IPv6). */
export function isIPLiteral(host) {
  if (typeof host !== "string" || host === "") return false;
  return host.includes(":") || (/^\d{1,3}(\.\d{1,3}){3}$/.test(host) && host.split(".").every((n) => Number(n) <= 255));
}

/**
 * Secondary address line for route headers (MTR and Traceroute).
 * Returns null when the target's host label is already an IP literal or
 * already displays the resolved IP (including --resolve-all "host (ip)" labels).
 */
export function targetAddress(target) {
  const host = (target?.host ?? "").trim();
  const ip = (target?.ip ?? "").trim();
  if (isIPLiteral(host)) return null;
  if (ip) {
    const hostLower = host.toLowerCase();
    const ipLower = ip.toLowerCase();
    if (hostLower === ipLower || hostLower.endsWith(`(${ipLower})`)) return null;
  }
  return ip || "Resolving address…";
}

