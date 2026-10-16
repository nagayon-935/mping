// One SSE stream drives the monitoring workspace and independent global pages.
import { el, fetchJSON, readSession, selectionWithin, writeSession } from "./dom.js";
import { renderTargets } from "./table.js";
import { createGraphs } from "./graphs.js";
import { createInspect } from "./inspect.js";
import { renderMTR } from "./mtr.js";
import { renderTraceroutes } from "./traceroute.js";
import { createLog } from "./log.js";
import { httpFor } from "./checks.js";
import { reconcileWindow } from "./timeline.js";
import { createControl } from "./control.js";
import { createReportDialog } from "./report.js";
import { createConfirmation } from "./dialogs.js";
import { createNotice } from "./notice.js";
import { createActionsMenu } from "./actions_menu.js";
import { createAddDialog, createAuthDialog } from "./control_dialogs.js";
import { installShortcuts } from "./shortcuts.js";
import { createActions } from "./actions.js";
import { measurementState, renderHeader } from "./header.js";
import { STATUSES, STATUS_LABELS, healthFilterItems, parseSort, sortOptions } from "./toolbar.js";
import { configuredTargets, sortTargets, frozenOrder, filterTargets } from "./workspace.js";

const HISTORY_EVERY_MS = 2000;
const MAX_HISTORY_POINTS = 3000; // the server's per-target RTT ring
const FILTER_KEY = "mping.filter";
const WINDOW_KEY = "mping.window";
const SCALE_KEY = "mping.sharedScale";
const STATUS_KEY = "mping.status";
const SORT_KEY = "mping.sort";
const SHORTCUTS_KEY = "mping.shortcuts";

const $ = (id) => document.getElementById(id);

// Must run before anything reads location.hash: it consumes "#token=".
const control = createControl();
const notify = createNotice({ node: $("notice"), text: $("notice-message"), dismiss: $("dismiss-notice") });
const menu = createActionsMenu({ toggle: $("toggle-actions"), menu: $("measurement-controls") });

const view = {
  snapshot: null,
  meta: null,
  state: null,
  connected: false,
  filter: "",
  selectedId: null,
  series: new Map(), // id -> samples for the graphs window
  windows: [],
  window: null,
  shared: readSession(SCALE_KEY) !== "false",
  inspectOpen: false,
  historyNow: null,
  page: "monitor",
  status: readSession(STATUS_KEY) || "all",
  sort: { key: "config", direction: "asc" },
  order: null, // frozen target IDs for a non-config sort
  focusedId: null,
};

const hasTarget = (id) => !!view.snapshot?.targets.some((t) => t.id === id);
const monitorHash = () => (view.selectedId == null ? "#monitor" : `#target-${view.selectedId}`);

function selectTarget(id) {
  if (!hasTarget(id)) return;
  view.selectedId = id;
  view.inspectOpen = true;
  view.page = "monitor";
  history.replaceState(null, "", `#target-${id}`);
  scheduleRender();
}

function clearSelection() {
  view.selectedId = null;
  view.inspectOpen = false;
  inspect.clear();
  if (location.hash) history.replaceState(null, "", location.pathname);
  scheduleRender();
}

function showMonitorPage() {
  view.page = "monitor";
  history.replaceState(null, "", monitorHash());
  scheduleRender();
}

const graphs = createGraphs({ grid: $("graphs"), cursorNote: $("graphs-cursor"), onSelect: selectTarget });

const inspect = createInspect({
  target: $("inspect-target"),
  body: $("inspect-body"),
  canControl: () => control.allowed,
  actionsFor(t) {
    if (!control.allowed) return null;
    const button = el("button", { className: "btn btn-danger", text: `Delete ${t.host}`, attrs: { type: "button" } });
    button.addEventListener("click", () => actions.deleteTarget(t.id));
    return button;
  },
});

const log = createLog({ body: $("log-body"), status: $("log-status"), requestRender: scheduleRender });

const report = createReportDialog({
  dialog: $("report-dialog"), form: $("report-form"), format: $("report-format"),
  name: $("report-name"), error: $("report-error"), cancel: $("cancel-report"), submit: $("download-report"),
  download: (...args) => control.downloadReport(...args),
  canControl: () => control.allowed,
  onError(message) {
    if (!control.allowed) notify(message, true);
    renderControls();
  },
});

const confirmation = createConfirmation({
  dialog: $("confirm-dialog"), form: $("confirm-form"), title: $("confirm-title"), description: $("confirm-description"),
  error: $("confirm-error"), cancel: $("confirm-cancel"), submit: $("confirm-action"),
});

const auth = createAuthDialog({
  dialog: $("auth-dialog"), form: $("auth-form"), token: $("auth-token"), show: $("auth-show"),
  error: $("auth-error"), submit: $("auth-submit"), explanation: $("readonly-explanation"),
}, {
  control,
  onEnabled() {
    notify("Browser controls enabled.");
    afterControlChange();
  },
});

const addHost = createAddDialog({
  dialog: $("add-dialog"), form: $("add-form"), host: $("add-host"), error: $("add-error"), submit: $("add-submit"),
}, { control, notify, onChange: afterControlChange, requestAuth: auth.open });

const actions = createActions({
  control, confirmation, notify,
  button: $("measurement-action"),
  findTarget: (id) => view.snapshot?.targets.find((t) => t.id === id),
  isStopped: () => ["stopped", "finished"].includes(measurementState(view)),
  onDeleted(target) {
    if (view.selectedId === target.id) clearSelection();
    requestAnimationFrame(() => $("filter").focus());
  },
  async onReset() {
    view.series = new Map();
    await refreshHistory();
  },
  async onMeasurementChange(ok) {
    if (ok) await reloadSnapshot();
    await refreshHistory();
  },
  afterChange: afterControlChange,
});

// --- Pages and ordering ----------------------------------------------------

function readPage() {
  view.page = location.hash === "#log" ? "log" : location.hash === "#http" ? "http" : "monitor";
  const wanted = /^#target-(\d+)$/.exec(location.hash);
  if (wanted && view.snapshot) selectTarget(Number(wanted[1]));
  scheduleRender();
}

function renderPages() {
  if (view.page === "http" && view.meta && !view.meta.features.http) {
    view.page = "monitor";
    history.replaceState(null, "", "#monitor");
  }
  for (const page of ["monitor", "log", "http"]) {
    $(page === "monitor" ? "workspace" : `page-${page}`).hidden = view.page !== page;
    const link = $(`nav-${page}`);
    if (view.page === page) link.setAttribute("aria-current", "page");
    else link.removeAttribute("aria-current");
  }
  $("nav-monitor").href = monitorHash();
  $("nav-http").hidden = !view.meta?.features.http;
  log.activate(view.page === "log");
}

function baseTargets() {
  return configuredTargets(view.snapshot.targets, view.meta.groups);
}

const sortedIds = (targets) => sortTargets(targets, view.sort, view.meta.thresholds).map((t) => t.id);

function refreshOrder() {
  if (!view.snapshot) return;
  view.order = sortedIds(baseTargets());
  scheduleRender();
}

function setSort(key, direction) {
  view.sort = { key, direction: direction || (["host", "ip", "config"].includes(key) ? "asc" : "desc") };
  writeSession(SORT_KEY, JSON.stringify(view.sort));
  $("sort-order").value = `${view.sort.key}:${view.sort.direction}`;
  refreshOrder();
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
  $("status-filter").value = status;
  writeSession(STATUS_KEY, status);
  if (view.page !== "monitor") showMonitorPage();
  else scheduleRender();
}

function clearFilters() {
  view.filter = "";
  $("filter").value = "";
  writeSession(FILTER_KEY, "");
  setStatus("all");
}

function visibleTargets() {
  const base = baseTargets();
  if (view.order === null) view.order = sortedIds(base);
  const ordered = view.sort.key === "config" ? base : frozenOrder(base, view.order);
  return filterTargets(ordered, view.filter, view.status, view.meta);
}

// --- Rendering -------------------------------------------------------------

// Rebuilding a table or the inspect pane drops any text the user is
// selecting, so those parts wait while a selection is open inside them;
// "selectionchange" re-runs the render once it is cleared.
let heldBySelection = false;

function unlessSelecting(node, render) {
  if (selectionWithin(node)) heldBySelection = true;
  else render();
}

function renderBody() {
  heldBySelection = false;
  renderPages();
  if (view.page === "log") {
    unlessSelecting($("log-body"), log.render);
    return;
  }
  if (!view.snapshot) {
    $("targets-empty-message").textContent = view.connected ? "Waiting for the first measurement…" : "Connecting to mping…";
    $("empty-action").hidden = true;
    $("targets-empty").hidden = false;
    return;
  }
  if (view.selectedId != null && !hasTarget(view.selectedId)) {
    clearSelection(); // deleted, or gone after a reload
  }
  if (view.page === "http") unlessSelecting($("http-body"), renderHttpPage);
  else renderMonitorPage();
}

function renderHttpPage() {
  // Keep the existing scroll container so live updates preserve both axes.
  const body = $("http-body");
  const top = body.scrollTop;
  const old = body.querySelector(".table-wrap");
  const left = old?.scrollLeft ?? 0;
  const next = httpFor(view.snapshot.http_checks);
  if (old && next.classList.contains("table-wrap")) {
    old.replaceChildren(...next.childNodes);
    old.scrollLeft = left;
  } else {
    body.replaceChildren(next);
  }
  body.scrollTop = top;
}

function renderMonitorPage() {
  const targets = visibleTargets();
  const total = view.snapshot.targets.length;
  const { features } = view.meta;
  const showMTR = !!features.mtr;
  const showTrace = !!features.traceroute && !showMTR;

  $("pane-inspect").hidden = !view.inspectOpen;
  $("workspace").classList.toggle("has-inspect", view.inspectOpen);
  $("pane-mtr").hidden = !showMTR;
  $("pane-traceroute").hidden = !showTrace;
  $("workspace").classList.toggle("has-route", showMTR || showTrace);

  if (showMTR) renderMTRPane(targets, total);
  if (showTrace) renderTraceroutePane(targets);
  renderTargetsPane(targets, total);
  renderMonitorChrome(targets, total, showMTR || showTrace);
  graphs.render({
    targets,
    history: view.series,
    meta: view.meta,
    points: view.window?.points ?? 0,
    shared: view.shared,
    selectedId: view.selectedId,
    now: view.historyNow,
  });
  unlessSelecting($("inspect-body"), () => { if (view.inspectOpen) inspect.render(view); });
}

const hostCount = (n) => `${n} ${n === 1 ? "host" : "hosts"} · ICMP`;

function renderMTRPane(targets, total) {
  $("mtr-summary").textContent = hostCount(targets.length);
  unlessSelecting($("mtr-scroll"), () => {
    const groups = renderMTR($("mtr"), targets, view.meta, view.selectedId);
    $("mtr-table-wrap").hidden = groups === 0;
    $("mtr-empty").hidden = groups > 0;
    $("mtr-empty").textContent = total === 0 ? "No targets." : "No targets match the filter.";
  });
}

function renderTraceroutePane(targets) {
  $("traceroute-summary").textContent = hostCount(targets.length);
  unlessSelecting($("traceroute-scroll"), () =>
    renderTraceroutes($("traceroute-scroll"), targets, view.meta.features, view.selectedId));
}

function emptyTargetsMessage(total) {
  if (total === 0) return "No hosts are being monitored. Add a host here or in the TUI / hosts file.";
  const query = view.filter ? `“${view.filter}” and ` : "";
  return `No hosts match ${query}${STATUS_LABELS[view.status] ?? "the current filters"}.`;
}

function renderTargetsPane(targets, total) {
  const { features } = view.meta;
  const fields = ["host", "IP", ...(features.ptr ? ["PTR"] : []), ...(features.asn ? ["AS"] : [])].join(", ");
  $("filter").placeholder = `Filter ${fields}`;
  $("filter").setAttribute("aria-label", `Filter ${fields}`);
  unlessSelecting($("targets"), () => {
    const shown = renderTargets($("targets"), { ...view, visibleTargets: targets }, selectTarget, sortColumn);
    $("targets-empty").hidden = shown > 0;
    $("targets-empty-message").textContent = emptyTargetsMessage(total);
    $("empty-action").hidden = false;
    $("empty-action").textContent = total ? "Clear filters" : control.allowed ? "Add host" : "Enable controls";
  });
}

/** Counts, sort state, locate buttons and hints around the monitor panes. */
function renderMonitorChrome(targets, total, routeShown) {
  $("shown-count").textContent = `${targets.length} / ${total} hosts`;

  const base = baseTargets();
  const upToDate = sortedIds(base).join() === frozenOrder(base, view.order).map((t) => t.id).join();
  const refresh = $("refresh-sort");
  refresh.disabled = view.sort.key === "config" || upToDate;
  refresh.textContent = refresh.disabled ? "Order up to date" : "Refresh order •";

  const selectedVisible = view.selectedId != null && targets.some((t) => t.id === view.selectedId);
  $("selection-note").hidden = view.selectedId == null || selectedVisible;
  for (const id of ["locate-mtr", "locate-traceroute", "locate-graphs"]) {
    $(id).disabled = !selectedVisible;
    $(id).title = selectedVisible ? "Scroll to the selected host without changing order." : "Select a host visible in the current filters.";
  }

  $("graphs-empty").hidden = targets.length > 0;
  $("graphs-empty").textContent = total ? "No hosts match the current filters." : "Add a host to start collecting RTT history.";
  const hint = `Loss % is lifetime loss; health uses recent measurements.${routeShown ? "" : " Route monitoring is disabled (start with -M or -T)."}`;
  $("monitor-hint").textContent = hint;
  $("monitor-hint").title = hint;
}

let renderQueued = false;
function scheduleRender() {
  if (renderQueued) return;
  renderQueued = true;
  requestAnimationFrame(() => {
    renderQueued = false;
    renderHeader(view);
    renderBody();
  });
}

document.addEventListener("selectionchange", () => {
  if (heldBySelection) scheduleRender();
});

// --- Data ------------------------------------------------------------------

function applySnapshot(body) {
  // The window choices depend on the probe interval, which a hosts-file
  // reload can change.
  const intervalChanged = view.meta?.interval_ms !== body.meta.interval_ms;
  view.snapshot = body.snapshot;
  view.meta = body.meta;
  view.state = body.state;
  renderControls();
  if (intervalChanged) {
    initWindows();
    refreshHistory();
  }
  scheduleRender();
  // Restore a selection from the address (reload or a shared link); a link
  // to a target that no longer exists is dropped rather than kept forever.
  const wanted = /^#target-(\d+)$/.exec(location.hash);
  if (wanted && view.selectedId == null) {
    const id = Number(wanted[1]);
    if (hasTarget(id)) selectTarget(id);
    else history.replaceState(null, "", location.pathname);
  }
}

function connect() {
  const stream = new EventSource("api/v1/stream");
  stream.addEventListener("open", () => {
    view.connected = true;
    control.refresh().then(afterControlChange);
    scheduleRender();
  });
  stream.addEventListener("snapshot", (e) => {
    try {
      applySnapshot(JSON.parse(e.data));
    } catch (err) {
      console.error("mping: bad snapshot event", err);
    }
  });
  stream.addEventListener("error", () => {
    // EventSource retries on its own; once mping reports "stopped" the
    // server is going away for good, so stop retrying.
    view.connected = false;
    if (view.state === "stopped") stream.close();
    scheduleRender();
  });
}

async function refreshHistory() {
  if (document.hidden || !view.snapshot || !view.window || view.state === "stopped") return;
  try {
    const body = await fetchJSON(`api/v1/history?n=${view.window.points}`);
    view.series = new Map(body.targets.map((s) => [s.id, s.samples?.length ? s.samples : s.rtt_ms]));
    const captured = body.captured_at || body.targets[0]?.captured_at;
    view.historyNow = captured ? Date.parse(captured) : Date.now();
    scheduleRender();
  } catch {
    // Transient (e.g. mping between reloads); the next tick retries.
  }
}

async function reloadSnapshot() {
  try {
    applySnapshot(await fetchJSON("api/v1/snapshot"));
  } catch {
    // The stream delivers the same state shortly.
  }
}

// --- Graph tools -----------------------------------------------------------

function initWindows() {
  const { options, window } = reconcileWindow(view.window?.label ?? readSession(WINDOW_KEY), view.meta.interval_ms, MAX_HISTORY_POINTS);
  view.windows = options;
  view.window = window;
  $("graph-window").replaceChildren(...view.windows.map((w) =>
    el("option", { text: w.label, attrs: { value: w.label, ...(w === view.window ? { selected: "" } : {}) } })));
}

function initGraphTools() {
  $("graph-window").addEventListener("change", (e) => {
    view.window = view.windows.find((w) => w.label === e.target.value) ?? view.window;
    writeSession(WINDOW_KEY, view.window.label);
    refreshHistory();
  });
  const setShared = (shared) => {
    view.shared = shared;
    writeSession(SCALE_KEY, String(shared));
    $("scale-each").setAttribute("aria-pressed", String(!shared));
    $("scale-shared").setAttribute("aria-pressed", String(shared));
    scheduleRender();
  };
  $("scale-each").addEventListener("click", () => setShared(false));
  $("scale-shared").addEventListener("click", () => setShared(true));
  setShared(view.shared);

  // Locate scrolls a pane to the selected host without reordering anything.
  const locators = [
    ["locate-mtr", () => $("mtr-table-wrap"), "#mtr tbody", 36], // below the sticky column header
    ["locate-traceroute", () => $("traceroute-scroll"), ".trace-target", 0],
    ["locate-graphs", () => $("graphs").parentElement, ".graph-card", 0],
  ];
  for (const [button, scroller, selector, offset] of locators) {
    $(button).addEventListener("click", () => {
      const node = document.querySelector(`${selector}[data-target-id="${view.selectedId}"]`);
      if (!node) return;
      const scroll = scroller();
      scroll.scrollTop += node.getBoundingClientRect().top - scroll.getBoundingClientRect().top - offset;
    });
  }
}

// --- Controls --------------------------------------------------------------

function renderControls() {
  const on = control.allowed;
  menu.setAvailable(on);
  $("open-auth").textContent = on ? "Controls enabled" : "Enable controls";
  $("open-auth").disabled = on;
  if (!on) {
    report.close();
    addHost.close();
    document.querySelector('#inspect-body [data-section="actions"]')?.remove();
  }
}

function afterControlChange() {
  renderControls();
  scheduleRender();
}

function initControls() {
  $("refresh-log").addEventListener("click", log.refresh);
  $("close-inspect").addEventListener("click", () => {
    clearSelection();
    $("filter").focus();
  });
  $("open-report").addEventListener("click", report.open);
  $("open-add").addEventListener("click", addHost.open);
  $("open-auth").addEventListener("click", auth.open);
  $("open-shortcuts").addEventListener("click", () => $("shortcuts-dialog").showModal());
  for (const button of document.querySelectorAll("[data-close]")) {
    button.addEventListener("click", () => $(button.dataset.close).close());
  }
  $("shortcuts-enabled").checked = readSession(SHORTCUTS_KEY) !== "false";
  $("shortcuts-enabled").addEventListener("change", () =>
    writeSession(SHORTCUTS_KEY, String($("shortcuts-enabled").checked)));
  $("measurement-action").addEventListener("click", actions.toggleMeasurements);

  const reset = el("button", { className: "btn btn-danger", text: "Reset stats", attrs: { type: "button", "aria-haspopup": "dialog" } });
  reset.addEventListener("click", actions.confirmReset);
  $("reset-slot").replaceChildren(reset);

  renderControls();
  control.refresh().then(afterControlChange);
}

// --- Monitor toolbar -------------------------------------------------------

function initFilter() {
  $("filter").value = readSession(FILTER_KEY) ?? "";
  view.filter = $("filter").value;
  $("filter").addEventListener("input", () => {
    view.filter = $("filter").value;
    writeSession(FILTER_KEY, view.filter);
    scheduleRender();
  });

  if (!STATUSES.includes(view.status)) view.status = "all";
  $("status-filter").value = view.status;
  $("status-filter").addEventListener("change", (event) => setStatus(event.target.value));
  $("summary").replaceChildren(...healthFilterItems(view.status, setStatus));

  view.sort = parseSort(readSession(SORT_KEY)) ?? view.sort;
  $("sort-order").append(...sortOptions());
  $("sort-order").value = `${view.sort.key}:${view.sort.direction}`;
  $("sort-order").addEventListener("change", (event) => {
    const [key, direction] = event.target.value.split(":");
    setSort(key, direction);
  });
  $("refresh-sort").addEventListener("click", refreshOrder);
  $("clear-filter").addEventListener("click", clearFilters);
  $("empty-action").addEventListener("click", () => (view.snapshot?.targets.length ? clearFilters() : addHost.open()));

  // Roving tabindex: only the focused host's button is in the tab order.
  $("targets").addEventListener("focusin", (event) => {
    const row = event.target.closest(".target-row");
    if (!row) return;
    view.focusedId = Number(row.dataset.targetId);
    for (const button of $("targets").querySelectorAll(".host-open")) {
      button.tabIndex = button.dataset.targetId === row.dataset.targetId ? 0 : -1;
    }
  });

  installShortcuts({
    table: $("targets"),
    filter: $("filter"),
    menu,
    charactersEnabled: () => $("shortcuts-enabled").checked,
    canControl: () => control.allowed,
    focusFilter() {
      showMonitorPage();
      requestAnimationFrame(() => $("filter").focus());
    },
    openHelp: () => $("shortcuts-dialog").showModal(),
    openReport: report.open,
    deleteTarget: actions.deleteTarget,
  });
}

// --- Startup ---------------------------------------------------------------

// Which columns fit depends on each table's width, which changes with the
// window, the pane layout and the scrollbar, not only on "resize".
function rerenderOnWidthChange(node) {
  let width = 0;
  new ResizeObserver(([entry]) => {
    const next = Math.round(entry.contentRect.width);
    if (next !== width) {
      width = next;
      scheduleRender();
    }
  }).observe(node);
}
rerenderOnWidthChange($("targets").parentElement);
rerenderOnWidthChange($("mtr-table-wrap"));
new ResizeObserver(() => graphs.repaint()).observe($("graphs"));

initFilter();
initGraphTools();
window.addEventListener("hashchange", readPage);
readPage();
window.addEventListener("resize", scheduleRender);
initControls();
connect();
scheduleRender();
setInterval(refreshHistory, HISTORY_EVERY_MS);
