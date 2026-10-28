// One SSE stream drives the monitoring workspace and independent global pages.
import { $, el, fetchJSON, readSession, selectionWithin, writeSession } from "./dom.js";
import { createGraphs } from "./graphs.js";
import { createInspect } from "./inspect.js";
import { createLog } from "./log.js";
import { httpFor } from "./checks.js";
import { createControl } from "./control.js";
import { createReportDialog } from "./report.js";
import { createConfirmation } from "./dialogs.js";
import { createNotice } from "./notice.js";
import { createActionsMenu } from "./actions_menu.js";
import { createAddDialog, createAuthDialog } from "./control_dialogs.js";
import { installShortcuts } from "./shortcuts.js";
import { createActions } from "./actions.js";
import { measurementState, renderHeader } from "./header.js";
import { createMonitorToolbar } from "./toolbar.js";
import { createTargetOrder } from "./workspace.js";
import { createWorkspaceView } from "./workspace_view.js";
import { createGraphTools } from "./graph_tools.js";

const HISTORY_EVERY_MS = 2000;
const SHORTCUTS_KEY = "mping.shortcuts";

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
  shared: true, // restored by graphTools.install() before the first render
  inspectOpen: false,
  historyNow: null,
  page: "monitor",
  status: "all",
  sort: { key: "config", direction: "asc" },
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

const log = createLog({
  body: $("log-body"),
  status: $("log-status"),
  requestRender: scheduleRender,
  isStopped: () => view.state === "stopped",
});

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

const toolbar = createMonitorToolbar(view, {
  filter: $("filter"), status: $("status-filter"), summary: $("summary"), order: $("sort-order"),
  refresh: $("refresh-sort"), clear: $("clear-filter"), empty: $("empty-action"), table: $("targets"),
}, {
  requestRender: scheduleRender,
  onOrderChange: refreshOrder,
  onStatusChange() {
    if (view.page !== "monitor") showMonitorPage();
    else scheduleRender();
  },
  openAddHost: addHost.open,
});

const workspace = createWorkspaceView({
  graphs, inspect, selectTarget, sortColumn: toolbar.sortColumn,
  canControl: () => control.allowed,
  renderUnlessSelecting: unlessSelecting,
  selectedId: () => view.selectedId,
});

const graphTools = createGraphTools(view, {
  window: $("graph-window"), perTarget: $("scale-each"), shared: $("scale-shared"),
}, { requestRender: scheduleRender, refreshHistory });

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

const targetOrder = createTargetOrder();

function refreshOrder() {
  if (!view.snapshot) return;
  targetOrder.refresh(view);
  scheduleRender();
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
  else workspace.render(view, targetOrder.derive(view));
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
    graphTools.updateWindows(view.meta.interval_ms);
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

// --- Keyboard shortcuts ----------------------------------------------------

function initShortcuts() {
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

toolbar.install();
initShortcuts();
graphTools.install();
window.addEventListener("hashchange", readPage);
readPage();
window.addEventListener("resize", scheduleRender);
initControls();
connect();
scheduleRender();
setInterval(refreshHistory, HISTORY_EVERY_MS);
