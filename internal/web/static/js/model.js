// Pure dashboard logic: no DOM, so it can be unit-tested under node.
// Threshold semantics mirror internal/ui (tui_helpers.go, mtr_view.go).

/** Level for a value against warn/crit, strictly greater-than like the TUI. */
export function levelAbove(value, warn, crit) {
  if (value == null || !(value > 0)) return "none";
  if (value > crit) return "crit";
  if (value > warn) return "warn";
  return "ok";
}

/** Loss rate in percent over completed probes, excluding in-flight ones. */
export function lossRate(t) {
  const done = t.recv + t.loss;
  return done === 0 ? 0 : (t.loss / done) * 100;
}

function lossLevel(t, th) {
  const rate = lossRate(t);
  if (rate > th.loss_crit_pct) return "crit";
  if (rate > th.loss_warn_pct) return "warn";
  return "ok";
}

const rank = { none: 0, pending: 0, ok: 1, warn: 2, crit: 3 };

function worst(...levels) {
  return levels.reduce((a, b) => (rank[b] > rank[a] ? b : a), "ok");
}

/** Overall status of a target row. */
export function rowLevel(t, th) {
  if (t.recv + t.loss === 0) return "pending";
  if (t.recv === 0) return "crit";
  return worst(
    lossLevel(t, th),
    levelAbove(t.last_rtt_ms, th.rtt_warn_ms, th.rtt_crit_ms),
    levelAbove(t.jitter_ms, th.jitter_warn_ms, th.jitter_crit_ms),
  );
}

/** Per-cell levels for the colour-coded columns. */
export function cellLevels(t, th) {
  const rtt = (v) => levelAbove(v, th.rtt_warn_ms, th.rtt_crit_ms);
  return {
    loss: t.recv + t.loss === 0 ? "none" : lossLevel(t, th),
    last: rtt(t.last_rtt_ms),
    avg: rtt(t.avg_rtt_ms),
    min: rtt(t.min_rtt_ms),
    max: rtt(t.max_rtt_ms),
    jitter: levelAbove(t.jitter_ms, th.jitter_warn_ms, th.jitter_crit_ms),
  };
}

/** MTR hop loss level: any loss warns, crit at or above the loss threshold. */
export function hopLossLevel(pct, th) {
  if (pct >= th.loss_crit_pct) return "crit";
  if (pct > 0) return "warn";
  return "ok";
}

/** Milliseconds with precision chosen by magnitude; "–" for no sample. */
export function formatMs(ms) {
  if (ms == null || !(ms > 0)) return "–";
  if (ms < 1) return ms.toFixed(3);
  if (ms < 10) return ms.toFixed(2);
  if (ms < 100) return ms.toFixed(1);
  return Math.round(ms).toString();
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

export function summarize(targets, th) {
  const out = { total: targets.length, ok: 0, warn: 0, crit: 0, pending: 0 };
  for (const t of targets) out[rowLevel(t, th)]++;
  return out;
}

export function matchesFilter(t, query) {
  const q = query.trim().toLowerCase();
  if (q === "") return true;
  return [t.host, t.ip, t.ptr, t.asn, t.org]
    .some((v) => typeof v === "string" && v.toLowerCase().includes(q));
}

/**
 * Header badge for the server-reported state and the stream connection.
 * A final "stopped" survives the disconnect that follows it.
 */
export function badgeFor(state, connected) {
  if (state === "stopped") return { level: "stopped", label: "Stopped" };
  if (!connected) {
    return state == null
      ? { level: "pending", label: "Connecting" }
      : { level: "crit", label: "Disconnected" };
  }
  switch (state) {
    case "running": return { level: "ok", label: "Live" };
    case "reloading": return { level: "warn", label: "Reloading" };
    default: return { level: "pending", label: "Starting" };
  }
}

// Mirrors pinger.dscpValueNames (CS0 wins the DF/CS0 alias).
const dscpNames = new Map([
  [46, "EF"], [44, "VA"], [0, "CS0"],
  [8, "CS1"], [16, "CS2"], [24, "CS3"], [32, "CS4"], [40, "CS5"], [48, "CS6"], [56, "CS7"],
  [10, "AF11"], [12, "AF12"], [14, "AF13"], [18, "AF21"], [20, "AF22"], [22, "AF23"],
  [26, "AF31"], [28, "AF32"], [30, "AF33"], [34, "AF41"], [36, "AF42"], [38, "AF43"],
]);

/** Codepoint name for an observed TOS/TrafficClass byte, like the TUI's DSCP column. */
export function dscpName(tos) {
  if (!(tos > 0)) return "–";
  const dscp = (tos >> 2) & 0x3f;
  return dscpNames.get(dscp) ?? String(dscp);
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
