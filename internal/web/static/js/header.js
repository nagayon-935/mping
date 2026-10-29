// Global status line: browser connection, measurement state, last probe and
// the per-health counts on the monitor's filter chips.
import { $, badgeIcon } from "./dom.js";
import { badgeFor, summarize } from "./model.js";
import { formatTime } from "./time.js";

/** The measurement state, also reported per target by older snapshots. */
export function measurementState(view) {
  return view.meta?.measurement_state ?? view.snapshot?.targets[0]?.measurement_state;
}

export function renderHeader(view) {
  const badge = badgeFor(view.state, view.connected);
  const node = $("badge");
  node.className = `badge lvl-${badge.level}`;
  node.querySelector(".badge-icon").textContent = badgeIcon(badge.level);
  node.querySelector(".badge-label").textContent = `Connection: ${badge.label}`;
  node.title = "Browser connection, independent of host health and measurement state.";

  const measurement = measurementState(view);
  const stopped = measurement === "stopped" || measurement === "finished";
  $("measurement-state").textContent = `Measurements: ${measurement || "waiting"}`;
  $("measurement-state").classList.toggle("is-stopped", stopped);
  const action = $("measurement-action");
  if (!action.disabled) action.textContent = stopped ? "Restart all" : "Stop all";
  action.title = "Stop or restart measurements for all hosts and enabled monitors. Existing statistics are retained.";

  const latest = view.snapshot?.targets.map((t) => t.health?.last_probe_at).filter(Boolean).sort().at(-1);
  $("updated").textContent = `Last probe ${latest ? formatTime(latest) : "–"}`;
  $("connection-note").textContent = !view.connected && view.snapshot
    ? view.state === "stopped" ? "Session ended; last values retained." : "Reconnecting; showing last received values."
    : "";

  const counts = view.snapshot
    ? summarize(view.snapshot.targets, view.meta.thresholds)
    : { total: 0, crit: 0, warn: 0, pending: 0, ok: 0 };
  for (const button of $("summary").querySelectorAll("button")) {
    const status = button.dataset.status;
    button.querySelector("strong").textContent = String(status === "all" ? counts.total : counts[status]);
    button.setAttribute("aria-pressed", String(view.status === status));
    button.disabled = !view.snapshot;
  }
}
