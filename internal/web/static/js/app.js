// Dashboard entry point: one SSE stream drives every render; sparkline
// history is polled in bulk alongside it.
import { el, badgeIcon, fetchJSON, selectionWithin } from "./dom.js";
import { renderHTTP, renderTargets } from "./table.js";
import { createDetail } from "./detail.js";
import { badgeFor, readOnlyHint, summarize, summaryItems, validateHost } from "./model.js";
import { confirmButton, createControl } from "./control.js";

const SPARK_POINTS = 60;
const SPARK_EVERY_MS = 2000;
const FILTER_KEY = "mping.filter";

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
  history: new Map(),
};

const detail = createDetail({
  root: $("detail"),
  title: $("detail-title"),
  sub: $("detail-sub"),
  body: $("detail-body"),
  closeButton: $("detail-close"),
  actionsFor(t) {
    if (!control.allowed) return null;
    return confirmButton(`Delete ${t.host}`, `Confirm delete ${t.host}?`, async () => {
      const res = await control.deleteTarget(t.id);
      if (res.ok) {
        notify(`Deleted ${t.host}.`);
        detail.close();
      } else {
        notify(`Couldn't delete ${t.host}: ${res.error}`, true);
        renderControls();
      }
    });
  },
  onClose() {
    const id = view.selectedId;
    view.selectedId = null;
    if (location.hash) history.replaceState(null, "", location.pathname);
    scheduleRender();
    document.querySelector(`tr[data-target-id="${id}"]`)?.focus();
  },
});

function openTarget(id) {
  const t = view.snapshot?.targets.find((x) => x.id === id);
  if (!t) return;
  view.selectedId = id;
  history.replaceState(null, "", `#target-${id}`);
  detail.open(id, t, view.meta);
  scheduleRender();
}

function renderHeader() {
  const badge = badgeFor(view.state, view.connected);
  const node = $("badge");
  node.className = `badge lvl-${badge.level}`;
  node.querySelector(".badge-icon").textContent = badgeIcon(badge.level);
  node.querySelector(".badge-label").textContent = badge.label;

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

// Rebuilding a table or the drawer drops any text the user is selecting, so
// those parts wait while a selection is open inside them; "selectionchange"
// re-runs the render once it is cleared.
let heldBySelection = false;

function renderBody() {
  heldBySelection = false;
  const empty = $("targets-empty");
  if (!view.snapshot) {
    empty.textContent = view.connected ? "Waiting for the first measurements…" : "Connecting to mping…";
    empty.hidden = false;
    return;
  }
  if (selectionWithin($("targets")) || selectionWithin($("http"))) {
    heldBySelection = true;
  } else {
    const shown = renderTargets($("targets"), view, openTarget);
    empty.hidden = shown > 0;
    empty.textContent = view.snapshot.targets.length === 0 ? "No targets." : "No targets match the filter.";
    $("http-panel").hidden = !renderHTTP($("http"), view.snapshot.http_checks);
  }

  if (detail.id != null) {
    if (selectionWithin($("detail"))) heldBySelection = true;
    else detail.update(view.snapshot.targets.find((t) => t.id === detail.id), view.meta);
  }
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
  view.snapshot = body.snapshot;
  view.meta = body.meta;
  view.state = body.state;
  scheduleRender();
  const wanted = /^#target-(\d+)$/.exec(location.hash);
  if (wanted && detail.id == null) openTarget(Number(wanted[1]));
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

async function refreshSparklines() {
  if (document.hidden || !view.snapshot || view.state === "stopped") return;
  try {
    const body = await fetchJSON(`api/v1/history?n=${SPARK_POINTS}`);
    view.history = new Map(body.targets.map((s) => [s.id, s.rtt_ms]));
    scheduleRender();
  } catch {
    // Transient (e.g. mping between reloads); the next tick retries.
  }
}

function renderControls() {
  const on = control.allowed;
  $("add-form").hidden = !on;
  $("reset-slot").hidden = !on;
  $("readonly-hint").hidden = on;
  // A rejected token (e.g. from an earlier mping run) also retracts the
  // drawer's delete button, not just the header controls.
  if (!on) document.querySelector('#detail-body [data-section="actions"]')?.remove();
}

function initControls() {
  const hint = readOnlyHint(location.hostname);
  $("readonly-hint").textContent = hint.text;
  $("readonly-hint").title = hint.title;
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

initFilter();
initControls();
connect();
scheduleRender();
setInterval(refreshSparklines, SPARK_EVERY_MS);
refreshSparklines();
