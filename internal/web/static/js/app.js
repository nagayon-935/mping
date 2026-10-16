// One SSE stream drives the monitoring workspace and independent global pages.
import { el, badgeIcon, fetchJSON, readSession, selectionWithin, writeSession } from "./dom.js";
import { renderTargets } from "./table.js";
import { createGraphs } from "./graphs.js";
import { createInspect } from "./inspect.js";
import { renderMTR } from "./mtr.js";
import { renderTraceroutes } from "./traceroute.js";
import { createLog } from "./log.js";
import { httpFor } from "./checks.js";
import { badgeFor, buildSections, matchesFilter, readOnlyHint, summarize, summaryItems, validateHost } from "./model.js";
import { reconcileWindow } from "./timeline.js";
import { confirmButton, createControl } from "./control.js";
import { formatTime } from "./time.js";
import { createReportDialog } from "./report.js";

const HISTORY_EVERY_MS = 2000;
const MAX_HISTORY_POINTS = 3000; // the server's per-target RTT ring
const FILTER_KEY = "mping.filter";
const WINDOW_KEY = "mping.window";
const SCALE_KEY = "mping.sharedScale";

const $ = (id) => document.getElementById(id);

// Must run before anything reads location.hash: it consumes "#token=".
const control = createControl();

let noticeTimer = null;
function notify(message, isError = false) {
  const node = $("notice");
  clearTimeout(noticeTimer);
  node.textContent = message;
  node.classList.toggle("is-error", isError);
  noticeTimer = setTimeout(() => { node.textContent = ""; }, isError ? 10000 : 4000);
}

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
};

function selectTarget(id) {
  if (!view.snapshot?.targets.some((t) => t.id === id)) return;
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

const graphs = createGraphs({ grid: $("graphs"), cursorNote: $("graphs-cursor"), onSelect: selectTarget });

const inspect = createInspect({
  target: $("inspect-target"),
  body: $("inspect-body"),
  canControl: () => control.allowed,
  actionsFor(t) {
    if (!control.allowed) return null;
    return confirmButton(`Delete ${t.host}`, `Confirm delete ${t.host}?`, async () => {
      const res = await control.deleteTarget(t.id);
      if (res.ok) {
        notify(`Deleted ${t.host}.`);
        clearSelection();
      } else {
        notify(`Couldn't delete ${t.host}: ${res.error}`, true);
        renderControls();
      }
    });
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
  $("nav-monitor").href = view.selectedId == null ? "#monitor" : `#target-${view.selectedId}`;
  $("nav-http").hidden = !view.meta?.features.http;
  $("filter").hidden = view.page !== "monitor";
  log.activate(view.page === "log");
}

/** Targets in display order (groups, then filter), shared by table and graphs. */
function visibleTargets() {
  const filtered = view.snapshot.targets.filter((t) => matchesFilter(t, view.filter, view.meta.features));
  return buildSections(filtered, view.meta.groups).flatMap((s) => s.targets);
}

function renderHeader() {
  const badge = badgeFor(view.state, view.connected);
  const node = $("badge");
  node.className = `badge lvl-${badge.level}`;
  node.querySelector(".badge-icon").textContent = badgeIcon(badge.level);
  node.querySelector(".badge-label").textContent = badge.label;
  node.title = "Dashboard connection and session state. Target health is shown in the table.";

  const measurement = view.meta?.measurement_state ?? view.snapshot?.targets[0]?.measurement_state;
  $("measurement-state").textContent = measurement ? `Measurements: ${measurement}` : "";
  const stopped = measurement === "stopped" || measurement === "finished";
  $("measurement-action").textContent = stopped ? "Restart" : "Stop";
  // Snapshots are only sent when something changed, so this is the time
  // of the last change, not of the last check.
  $("updated").textContent = view.snapshot
    ? `Last change ${formatTime(view.snapshot.timestamp)}`
    : "";

  const summary = $("summary");
  if (!view.snapshot) {
    summary.replaceChildren();
    return;
  }
  const s = summarize(view.snapshot.targets, view.meta.thresholds);
  summary.replaceChildren(...summaryItems(s).map(([n, label]) =>
    el("li", {}, el("strong", { text: String(n) }), el("span", { text: label }))));
}

// Rebuilding a table or the inspect pane drops any text the user is
// selecting, so those parts wait while a selection is open inside them;
// "selectionchange" re-runs the render once it is cleared.
let heldBySelection = false;

function renderBody() {
  heldBySelection = false;
  renderPages();
  if (view.page === "log") {
    if (selectionWithin($("log-body"))) heldBySelection = true;
    else log.render();
    return;
  }
  const empty = $("targets-empty");
  if (!view.snapshot) {
    empty.textContent = view.connected ? "Waiting for the first measurements…" : "Connecting to mping…";
    empty.hidden = false;
    return;
  }
  if (view.selectedId != null && !view.snapshot.targets.some((t) => t.id === view.selectedId)) {
    clearSelection(); // deleted, or gone after a reload
  }
  if (view.page === "http") {
    if (selectionWithin($("http-body"))) heldBySelection = true;
    else {
      const body = $("http-body");
      const top = body.scrollTop;
      const old = body.querySelector(".table-wrap");
      const left = old?.scrollLeft ?? 0;
      const [next] = httpFor(view.snapshot.http_checks);
      if (old && next.classList.contains("table-wrap")) {
        old.replaceChildren(...next.childNodes);
        old.scrollLeft = left;
      } else body.replaceChildren(next);
      body.scrollTop = top;
    }
    return;
  }
  $("pane-inspect").hidden = !view.inspectOpen;
  $("workspace").classList.toggle("has-inspect", view.inspectOpen);
  const showMTR = !!view.meta.features.mtr;
  $("pane-mtr").hidden = !showMTR;
  const showTrace = !!view.meta.features.traceroute && !showMTR;
  $("pane-traceroute").hidden = !showTrace;
  $("workspace").classList.toggle("has-route", showMTR || showTrace);
  if (showMTR) {
    const targets = visibleTargets();
    $("mtr-summary").textContent = `${targets.length} ${targets.length === 1 ? "host" : "hosts"} · ICMP`;
    if (selectionWithin($("mtr-scroll"))) heldBySelection = true;
    else {
      const groups = renderMTR($("mtr"), targets, view.meta);
      $("mtr-table-wrap").hidden = groups === 0;
      $("mtr-empty").hidden = groups > 0;
      $("mtr-empty").textContent = view.snapshot.targets.length === 0 ? "No targets." : "No targets match the filter.";
    }
  }
  if (showTrace) {
    const targets = visibleTargets();
    $("traceroute-summary").textContent = `${targets.length} ${targets.length === 1 ? "host" : "hosts"} · ICMP`;
    if (selectionWithin($("traceroute-scroll"))) heldBySelection = true;
    else renderTraceroutes($("traceroute-scroll"), targets, view.meta.features);
  }
  const fields = ["host", "IP", ...(view.meta.features.ptr ? ["PTR"] : []), ...(view.meta.features.asn ? ["AS"] : [])].join(", ");
  $("filter").placeholder = `Filter ${fields}`;
  $("filter").setAttribute("aria-label", `Filter ${fields}`);
  if (selectionWithin($("targets"))) {
    heldBySelection = true;
  } else {
    const shown = renderTargets($("targets"), view, selectTarget);
    empty.hidden = shown > 0;
    empty.textContent = view.snapshot.targets.length === 0 ? "No targets." : "No targets match the filter.";
  }
  graphs.render({
    targets: visibleTargets(),
    history: view.series,
    meta: view.meta,
    points: view.window?.points ?? 0,
    shared: view.shared,
    selectedId: view.selectedId,
    now: view.historyNow,
  });
  if (selectionWithin($("inspect-body"))) heldBySelection = true;
  else if (view.inspectOpen) inspect.render(view);
}

document.addEventListener("selectionchange", () => {
  if (heldBySelection) scheduleRender();
});

let renderQueued = false;
function scheduleRender() {
  if (renderQueued) return;
  renderQueued = true;
  requestAnimationFrame(() => {
    renderQueued = false;
    renderHeader();
    renderBody();
  });
}

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
    if (view.snapshot.targets.some((t) => t.id === id)) selectTarget(id);
    else history.replaceState(null, "", location.pathname);
  }
}

function connect() {
  const stream = new EventSource("api/v1/stream");
  stream.addEventListener("open", () => {
    view.connected = true;
    control.refresh().then(renderControls);
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
    const n = view.window.points;
    const body = await fetchJSON(`api/v1/history?n=${n}`);
    view.series = new Map(body.targets.map((s) => [s.id, s.samples?.length ? s.samples : s.rtt_ms]));
    view.historyNow = body.captured_at ? Date.parse(body.captured_at) : (body.targets[0]?.captured_at ? Date.parse(body.targets[0].captured_at) : Date.now());
    scheduleRender();
  } catch {
    // Transient (e.g. mping between reloads); the next tick retries.
  }
}

function initWindows() {
  const { options, window } = reconcileWindow(view.window?.label ?? readSession(WINDOW_KEY), view.meta.interval_ms, MAX_HISTORY_POINTS);
  view.windows = options;
  view.window = window;
  const select = $("graph-window");
  select.replaceChildren(...view.windows.map((w) =>
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
}

function renderControls() {
  const on = control.allowed;
  $("add-form").hidden = !on;
  $("reset-slot").hidden = !on;
  $("measurement-controls").hidden = !on;
  $("readonly-help").hidden = on;
  if (!on) report.close();
  // A rejected token (e.g. from an earlier mping run) also retracts the
  // inspect pane's delete button, not just the header controls.
  if (!on) document.querySelector('#inspect-body [data-section="actions"]')?.remove();
}

function initControls() {
  $("refresh-log").addEventListener("click", log.refresh);
  $("close-inspect").addEventListener("click", () => {
    clearSelection();
    $("filter").focus();
  });
  $("measurement-action").addEventListener("click", async () => {
    const state = view.meta?.measurement_state ?? view.snapshot?.targets[0]?.measurement_state;
    const restart = state === "stopped" || state === "finished";
    $("measurement-action").disabled = true;
    try {
      const res = await (restart ? control.restart() : control.stop());
      notify(res.ok ? (restart ? "Measurements restarted." : "Measurements stopped.") : res.error, !res.ok);
      if (res.ok) {
        try { applySnapshot(await fetchJSON("api/v1/snapshot")); } catch { /* SSE will retry. */ }
      }
      await refreshHistory();
      renderControls();
    } finally { $("measurement-action").disabled = false; }
  });
  $("open-report").addEventListener("click", report.open);

  const hint = readOnlyHint(location.hostname);
  $("readonly-hint").textContent = `${hint.text} · How to enable`;
  $("readonly-hint").title = hint.title;
  $("readonly-explanation").textContent = `${hint.title} For a headless run, use the control link printed in the terminal, or the token configured with MPING_WEB_TOKEN.`;
  $("reset-slot").replaceChildren(confirmButton("Reset stats", "Confirm reset?", async () => {
    const res = await control.reset();
    notify(res.ok ? "Statistics reset." : `Couldn't reset: ${res.error}`, !res.ok);
    if (!res.ok) renderControls();
  }));
  let adding = false;
  $("add-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    // One request at a time: a quick second submit would otherwise come
    // back "already in the list" right after the first one succeeded.
    if (adding) return;
    const input = $("add-host");
    const checked = validateHost(input.value);
    if (checked.error) {
      notify(checked.error, true);
      input.focus();
      return;
    }
    adding = true;
    const button = $("add-form").querySelector("button");
    button.disabled = true;
    let res;
    try {
      res = await control.addHost(checked.host);
    } finally {
      adding = false;
      button.disabled = false;
    }
    if (res.ok) {
      notify(`Added ${checked.host}.`);
      input.value = "";
    } else {
      notify(`Couldn't add ${checked.host}: ${res.error}`, true);
      renderControls();
    }
  });
  renderControls();
  control.refresh().then(renderControls);
}

function initFilter() {
  const input = $("filter");
  input.value = readSession(FILTER_KEY) ?? "";
  view.filter = input.value;
  input.addEventListener("input", () => {
    view.filter = input.value;
    writeSession(FILTER_KEY, input.value);
    scheduleRender();
  });
}

// Which columns fit depends on the table's width, which changes with the
// window, the pane layout and the scrollbar, not only on "resize".
let tableWidth = 0;
new ResizeObserver(([entry]) => {
  const width = Math.round(entry.contentRect.width);
  if (width !== tableWidth) {
    tableWidth = width;
    scheduleRender();
  }
}).observe($("targets").parentElement);
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
