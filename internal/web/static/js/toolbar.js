// Building blocks of the monitor toolbar: header health pills and sort
// choices, plus their preferences and host-list focus.
import { el, badgeIcon, readSession, writeSession } from "./dom.js";
import { STATUS_LABELS } from "./model.js";
import { SORTS } from "./workspace.js";

export { STATUS_LABELS };
const STATUSES = ["all", ...Object.keys(STATUS_LABELS)];

/**
 * Header pills, one per health level (shape + count + label, never colour
 * alone). Each filters the monitor; pressing the active one shows all again.
 * Counts are filled by the header.
 */
const PILL_ORDER = ["crit", "warn", "pending", "ok"];
function healthFilterItems(current, onSelect, currentStatus) {
  return PILL_ORDER.map((status) => {
    const button = el("button", {
      className: `fleet-pill health-${status}`,
      attrs: { type: "button", "data-status": status, "aria-pressed": String(status === current),
        title: `Show only ${STATUS_LABELS[status]} hosts; press again to show all` },
    },
    el("span", { className: "fleet-icon", text: badgeIcon(status), attrs: { "aria-hidden": "true" } }),
    el("strong", { text: "0" }),
    el("span", { text: STATUS_LABELS[status] }));
    button.addEventListener("click", () => onSelect(currentStatus() === status ? "all" : status));
    return el("li", {}, button);
  });
}

/** Configured order has no direction; every other key offers ↓ then ↑. */
function sortOptions() {
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

const FILTER_KEY = "mping.filter";
const STATUS_KEY = "mping.status";
const SORT_KEY = "mping.sort";

/** Filter/sort preferences and host focus; measurement updates never rebind these controls. */
export function createMonitorToolbar(view, dom, { requestRender, onOrderChange, onStatusChange, openAddHost }) {
  function setSort(key, direction) {
    view.sort = { key, direction: direction || (["host", "ip", "config"].includes(key) ? "asc" : "desc") };
    writeSession(SORT_KEY, JSON.stringify(view.sort));
    dom.order.value = `${view.sort.key}:${view.sort.direction}`;
    onOrderChange();
  }

  /** Column headings cycle: first direction, the other one, then config order. */
  function sortColumn(key) {
    const first = ["host", "ip"].includes(key) ? "asc" : "desc";
    if (view.sort.key !== key) setSort(key, first);
    else if (view.sort.direction === first) setSort(key, first === "asc" ? "desc" : "asc");
    else setSort("config", "asc");
  }

  function setStatus(status) {
    view.status = status;
    dom.status.value = status;
    writeSession(STATUS_KEY, status);
    onStatusChange();
  }

  function clearFilters() {
    view.filter = "";
    dom.filter.value = "";
    writeSession(FILTER_KEY, "");
    setStatus("all");
  }

  function install() {
    dom.filter.value = readSession(FILTER_KEY) ?? "";
    view.filter = dom.filter.value;
    dom.filter.addEventListener("input", () => {
      view.filter = dom.filter.value;
      writeSession(FILTER_KEY, view.filter);
      requestRender();
    });

    view.status = readSession(STATUS_KEY) || view.status;
    if (!STATUSES.includes(view.status)) view.status = "all";
    dom.status.value = view.status;
    dom.status.addEventListener("change", (event) => setStatus(event.target.value));
    dom.summary.replaceChildren(...healthFilterItems(view.status, setStatus, () => view.status));

    view.sort = parseSort(readSession(SORT_KEY)) ?? view.sort;
    dom.order.append(...sortOptions());
    dom.order.value = `${view.sort.key}:${view.sort.direction}`;
    dom.order.addEventListener("change", (event) => {
      const [key, direction] = event.target.value.split(":");
      setSort(key, direction);
    });
    dom.refresh.addEventListener("click", onOrderChange);
    dom.clear.addEventListener("click", clearFilters);
    dom.empty.addEventListener("click", () => (view.snapshot?.targets.length ? clearFilters() : openAddHost()));

    // Roving tabindex: only the focused host's button is in the tab order.
    dom.table.addEventListener("focusin", (event) => {
      const row = event.target.closest(".target-row");
      if (!row) return;
      view.focusedId = Number(row.dataset.targetId);
      for (const button of dom.table.querySelectorAll(".host-open")) {
        button.tabIndex = button.dataset.targetId === row.dataset.targetId ? 0 : -1;
      }
    });
  }

  return { install, sortColumn };
}
