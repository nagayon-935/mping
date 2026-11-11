// Pure wording for health reasons and the header's connection indicator (no
// DOM, unit-tested under node). The server (internal/stats) decides health
// and explains it as "Jitter 50.5ms > 50.0ms · RTT 220.0ms > 200.0ms"; these
// helpers only reshape that text for compact places.

const BREACH = /^(Recent loss|RTT|Jitter) (\d+(?:\.\d+)?)(ms|%) > (\d+(?:\.\d+)?)(ms|%)$/;
const HEALTH = /^(ok|warn|crit|pending): (.*)$/;
const METRIC_NAMES = { "Recent loss": "Loss", RTT: "RTT", Jitter: "Jitter" };

/** A health transition event as {level, reason}, or null for other kinds. */
export function healthTransition(event) {
  if (event?.kind !== "health") return null;
  const m = HEALTH.exec(event.message ?? "");
  return m ? { level: m[1], reason: m[2] } : null;
}

/**
 * Threshold breaches named in a server health reason.
 * @returns {{metric: "Loss"|"RTT"|"Jitter", value: number, threshold: number, unit: "ms"|"%"}[]}
 */
export function parseBreaches(reason) {
  if (typeof reason !== "string") return [];
  return reason.split(" · ").flatMap((part) => {
    const m = BREACH.exec(part.trim());
    return m ? [{ metric: METRIC_NAMES[m[1]], value: Number(m[2]), threshold: Number(m[4]), unit: m[3] }] : [];
  });
}

/** "50.5 ms" / "25.0%": the unit style used everywhere else in the UI. */
export function withUnit(value, unit) {
  const text = Number.isInteger(value) ? `${value}.0` : String(value);
  return unit === "%" ? `${text}%` : `${text} ms`;
}

/**
 * What breached and its value ("Jitter 50.5 ms"); the threshold is left to
 * the title and Summary. Reasons that are not threshold breaches
 * ("3 consecutive failures") are kept as they are.
 */
export function shortReason(reason) {
  if (typeof reason !== "string" || reason === "") return "";
  return reason.split(" · ").map((part) => {
    const [breach] = parseBreaches(part);
    return breach ? `${breach.metric} ${withUnit(breach.value, breach.unit)}` : part.trim();
  }).join(" · ");
}

/**
 * Severity ("crit" or "warn") of a parsed breach. Uses the server's
 * per-metric level (or overall health status when it rules out one side for
 * recent Loss) before falling back to the global critical threshold.
 */
export function breachLevel(breach, thresholds = {}, levels = null, status = null) {
  const metricLevel = { Loss: null, RTT: levels?.last_rtt, Jitter: levels?.jitter }[breach.metric];
  if (metricLevel === "crit" || metricLevel === "warn") return metricLevel;
  if (status === "warn") return "warn";
  if (status === "crit" && levels?.last_rtt && levels.last_rtt !== "crit" && levels.jitter !== "crit") return "crit";
  const crit = { Loss: thresholds.loss_crit_pct, RTT: thresholds.rtt_crit_ms, Jitter: thresholds.jitter_crit_ms }[breach.metric];
  return crit > 0 && breach.value > crit ? "crit" : "warn";
}

/**
 * One sentence per breach for Summary: "Jitter 50.5 ms is over the 50.0 ms
 * critical threshold".
 */
export function explainBreach(breach, thresholds = {}, levels = null, status = null) {
  const level = breachLevel(breach, thresholds, levels, status) === "crit" ? "critical" : "warning";
  return {
    lead: `${breach.metric} ${withUnit(breach.value, breach.unit)}`,
    rest: `is over the ${withUnit(breach.threshold, breach.unit)} ${level} threshold`,
  };
}

/**
 * Approximate hop count from a reply TTL, assuming the sender started from
 * the nearest common initial TTL (32, 64, 128 or 255). null when unknown.
 */
export function ttlHops(ttl) {
  if (!(ttl > 0) || ttl > 255) return null;
  const initial = [32, 64, 128, 255].find((start) => ttl <= start);
  return initial - ttl;
}

/**
 * The header's compact connection indicator. It folds the browser
 * connection, the server state and the measurement state into one label
 * (with a level for the dot) and keeps the long explanation for the title.
 * @param {{state?: string|null, connected: boolean, measurement?: string, latestProbe?: string|null, hasSnapshot: boolean}} s
 * @returns {{level: "ok"|"warn"|"pending"|"stopped", label: string, detail: string}}
 */
export function connectionSummary({ state, connected, measurement, hasSnapshot }) {
  if (state === "stopped") {
    return { level: "stopped", label: "Session ended", detail: "mping has exited; the last received values are retained." };
  }
  if (!connected) {
    return state == null
      ? { level: "pending", label: "Connecting", detail: "Connecting to mping…" }
      : { level: "warn", label: "Reconnecting", detail: hasSnapshot ? "Connection lost; showing the last received values." : "Connection lost." };
  }
  if (state === "reloading") return { level: "warn", label: "Reloading", detail: "mping is reloading the hosts file." };
  if (state !== "running") return { level: "pending", label: "Starting", detail: "mping is starting." };
  if (measurement === "stopped" || measurement === "finished") {
    return { level: "stopped", label: measurement === "finished" ? "Finished" : "Stopped",
      detail: `Connected; measurements are ${measurement}. Existing statistics are retained.` };
  }
  return { level: "ok", label: "Live", detail: "Connected; measurements are running." };
}
