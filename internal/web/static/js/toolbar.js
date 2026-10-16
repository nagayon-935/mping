// Building blocks of the monitor toolbar: health filter chips and sort
// choices. State lives in app.js; these only create the controls.
import { el, badgeIcon } from "./dom.js";
import { SORTS } from "./workspace.js";

export const STATUS_LABELS = { crit: "Critical", warn: "Warning", pending: "Waiting", ok: "OK" };
export const STATUSES = ["all", ...Object.keys(STATUS_LABELS)];

/** One pressed-state button per health level; counts are filled by the header. */
export function healthFilterItems(current, onSelect) {
  return STATUSES.map((status) => {
    const button = el("button", {
      className: `health-filter health-${status}`,
      attrs: { type: "button", "data-status": status, "aria-pressed": String(status === current) },
    },
    el("span", { text: status === "all" ? "" : badgeIcon(status), attrs: { "aria-hidden": "true" } }),
    el("strong", { text: "0" }),
    el("span", { text: status === "all" ? "All" : STATUS_LABELS[status] }));
    button.addEventListener("click", () => onSelect(status));
    return el("li", {}, button);
  });
}

/** Configured order has no direction; every other key offers ↓ then ↑. */
export function sortOptions() {
  return Object.entries(SORTS).flatMap(([key, label]) =>
    (key === "config" ? ["asc"] : ["desc", "asc"]).map((direction) =>
      el("option", {
        text: key === "config" ? label : `${label} ${direction === "desc" ? "↓" : "↑"}`,
        attrs: { value: `${key}:${direction}` },
      })));
}

/** A stored sort, or null when it is missing, malformed or no longer offered. */
export function parseSort(raw) {
  try {
    const saved = JSON.parse(raw);
    if (saved && SORTS[saved.key] && ["asc", "desc"].includes(saved.direction)) return saved;
  } catch {
    // Fall through to the default.
  }
  return null;
}
