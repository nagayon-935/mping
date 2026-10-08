// Dashboard entry point: one SSE stream drives every render. The layout
// follows the TUI's panes: ping monitor and RTT graphs side by side, and an
// inspect pane (summary, path, ports, HTTP, log) below. Selecting a target
// anywhere points the per-target panes at it.
import { el, badgeIcon, fetchJSON, selectionWithin } from "./dom.js";
import { renderTargets } from "./table.js";
import { createGraphs } from "./graphs.js";
import { createInspect } from "./inspect.js";
import { badgeFor, buildSections, matchesFilter, readOnlyHint, summarize, summaryItems, validateHost } from "./model.js";
import { reconcileWindow, tail } from "./timeline.js";
import { confirmButton, createControl } from "./control.js";

const SPARK_POINTS = 60;
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

function readSession(key) {
  try {
    return sessionStorage.getItem(key);
  } catch {
    return null; // storage blocked: fall back to defaults
  }
}

function writeSession(key, value) {
  try {
    sessionStorage.setItem(key, value);
  } catch {
    // See readSession.
  }
}

const view = {
  snapshot: null,
  meta: null,
  state: null,
  connected: false,
  filter: "",
  selectedId: null,
  history: new Map(), // id -> newest SPARK_POINTS samples (table sparklines)
  series: new Map(), // id -> samples for the graphs window
  windows: [],
  window: null,
  shared: readSession(SCALE_KEY) === "true",
};

function selectTarget(id) {
  if (!view.snapshot?.targets.some((t) => t.id === id)) return;
  view.selectedId = id;
  history.replaceState(null, "", `#target-${id}`);
  scheduleRender();
}

function clearSelection() {
  if (view.selectedId == null) return;
  view.selectedId = null;
  if (location.hash) history.replaceState(null, "", location.pathname);
  scheduleRender();
}

const graphs = createGraphs({ grid: $("graphs"), cursorNote: $("graphs-cursor"), onSelect: selectTarget });

const inspect = createInspect({
  tabs: $("inspect-tabs"),
  target: $("inspect-target"),
  body: $("inspect-body"),
  onSelect: selectTarget,
  canControl: () => control.allowed,
  requestRender: () => scheduleRender(),
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

/** Targets in display order (groups, then filter), shared by table and graphs. */
function visibleTargets() {
  const filtered = view.snapshot.targets.filter((t) => matchesFilter(t, view.filter));
  return buildSections(filtered, view.meta.groups).flatMap((s) => s.targets);
}

function renderHeader() {
  const badge = badgeFor(view.state, view.connected);
  const node = $("badge");
  node.className = `badge lvl-${badge.level}`;
  node.querySelector(".badge-icon").textContent = badgeIcon(badge.level);
  node.querySelector(".badge-label").textContent = badge.label;
  node.title = "Dashboard connection and session state. Target health is shown in the table.";

  // Snapshots are only sent when something changed, so this is the time
  // of the last change, not of the last check.
  $("updated").textContent = view.snapshot
    ? `Last change ${new Date(view.snapshot.timestamp).toLocaleTimeString()}`
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
  const empty = $("targets-empty");
  if (!view.snapshot) {
    empty.textContent = view.connected ? "Waiting for the first measurements…" : "Connecting to mping…";
    empty.hidden = false;
    return;
  }
  if (view.selectedId != null && !view.snapshot.targets.some((t) => t.id === view.selectedId)) {
    clearSelection(); // deleted, or gone after a reload
  }
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
  });
  if (selectionWithin($("inspect-body"))) heldBySelection = true;
  else inspect.render(view);
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
  if (intervalChanged) {
    initWindows();
    refreshHistory();
  }
  scheduleRender();
  const wanted = /^#target-(\d+)$/.exec(location.hash);
  if (wanted && view.selectedId == null) selectTarget(Number(wanted[1]));
}

function connect() {
  const stream = new EventSource("api/v1/stream");
  stream.addEventListener("open", () => {
    view.connected = true;
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
    const n = Math.max(SPARK_POINTS, view.window.points);
    const body = await fetchJSON(`api/v1/history?n=${n}`);
    view.series = new Map(body.targets.map((s) => [s.id, s.rtt_ms]));
    view.history = new Map(body.targets.map((s) => [s.id, tail(s.rtt_ms, SPARK_POINTS)]));
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

/** j/k move the selection through the visible targets; Escape clears it. */
function initKeys() {
  document.addEventListener("keydown", (e) => {
    const t = e.target;
    if (t instanceof HTMLInputElement || t instanceof HTMLTextAreaElement || t instanceof HTMLSelectElement) return;
    if (e.metaKey || e.ctrlKey || e.altKey || !view.snapshot) return;
    if (e.key === "Escape") {
      clearSelection();
      return;
    }
    if (e.key !== "j" && e.key !== "k") return;
    const ids = visibleTargets().map((x) => x.id);
    if (ids.length === 0) return;
    const at = ids.indexOf(view.selectedId);
    const next = at < 0 ? 0 : Math.min(ids.length - 1, Math.max(0, at + (e.key === "j" ? 1 : -1)));
    e.preventDefault();
    selectTarget(ids[next]);
    requestAnimationFrame(() => document.querySelector(`#targets tr[data-target-id="${ids[next]}"]`)?.scrollIntoView({ block: "nearest" }));
  });
}

function renderControls() {
  const on = control.allowed;
  $("add-form").hidden = !on;
  $("reset-slot").hidden = !on;
  $("readonly-help").hidden = on;
  // A rejected token (e.g. from an earlier mping run) also retracts the
  // inspect pane's delete button, not just the header controls.
  if (!on) document.querySelector('#inspect-body [data-section="actions"]')?.remove();
}

function initControls() {
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
  try {
    input.value = sessionStorage.getItem(FILTER_KEY) ?? "";
  } catch {
    // Storage can be unavailable (privacy mode); filtering still works.
  }
  view.filter = input.value;
  input.addEventListener("input", () => {
    view.filter = input.value;
    try {
      sessionStorage.setItem(FILTER_KEY, input.value);
    } catch {
      // See above.
    }
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
initKeys();
window.addEventListener("resize", scheduleRender);
initControls();
connect();
scheduleRender();
setInterval(refreshHistory, HISTORY_EVERY_MS);
