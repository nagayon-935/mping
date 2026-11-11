// Global header: the fleet-health pills (which also filter the monitor) and
// one compact connection indicator folding the browser connection, the
// server state, the measurement state and the latest probe time.
import { $ } from "./dom.js";
import { summarize } from "./model.js";
import { connectionSummary } from "./health_text.js";
import { formatTime } from "./time.js";

/** The measurement state, also reported per target by older snapshots. */
export function measurementState(view) {
  return view.meta?.measurement_state ?? view.snapshot?.targets?.[0]?.measurement_state;
}

/** The newest probe timestamp across `targets`, or undefined when none is set. */
export function latestProbeAt(targets) {
  let latest;
  let latestMs = -Infinity;
  for (const t of targets ?? []) {
    const at = t.health?.last_probe_at;
    const ms = at ? Date.parse(at) : NaN;
    if (ms > latestMs) {
      latestMs = ms;
      latest = at;
    }
  }
  return latest;
}

export function renderHeader(view) {
  const measurement = measurementState(view);
  const latest = latestProbeAt(view.snapshot?.targets);
  const conn = connectionSummary({ state: view.state, connected: view.connected, measurement, hasSnapshot: !!view.snapshot });
  const node = $("connection");
  node.className = `conn lvl-${conn.level}`;
  // Only the label is a live region: the probe time changes every second.
  if ($("badge").textContent !== conn.label) $("badge").textContent = conn.label;
  // Without a live stream the values on screen are frozen: say so in place.
  const frozen = !view.connected || view.state === "stopped";
  $("updated").textContent = view.snapshot
    ? `· ${frozen ? "showing values from" : "last probe"} ${latest ? formatTime(latest) : "–"}` : "";
  node.title = `${conn.detail} Host health is shown separately.`;

  const stopped = measurement === "stopped" || measurement === "finished";
  const action = $("measurement-action");
  if (!action.disabled) action.textContent = stopped ? "Restart all" : "Stop all";
  action.title = "Stop or restart measurements for all hosts and enabled monitors. Existing statistics are retained.";

  const counts = view.snapshot
    ? summarize(view.snapshot.targets)
    : { total: 0, crit: 0, warn: 0, pending: 0, ok: 0 };
  for (const button of $("summary").querySelectorAll("button")) {
    const status = button.dataset.status;
    const count = counts[status] ?? 0;
    button.querySelector("strong").textContent = String(count);
    button.classList.toggle("is-zero", count === 0);
    button.setAttribute("aria-pressed", String(view.status === status));
    button.disabled = !view.snapshot;
    // Waiting only matters while some host is waiting (or it is the filter).
    if (status === "pending") button.parentElement.hidden = count === 0 && view.status !== "pending";
  }
}
