// Dashboard entry point: one SSE stream drives every render; sparkline
// history is polled in bulk alongside it.
import { el, badgeIcon, fetchJSON } from "./dom.js";
import { renderHTTP, renderTargets } from "./table.js";
import { createDetail } from "./detail.js";
import { badgeFor, summarize } from "./model.js";

const SPARK_POINTS = 60;
const SPARK_EVERY_MS = 2000;
const FILTER_KEY = "mping.filter";

const $ = (id) => document.getElementById(id);

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

  $("updated").textContent = view.snapshot
    ? `Updated ${new Date(view.snapshot.timestamp).toLocaleTimeString()}`
    : "";

  const summary = $("summary");
  if (!view.snapshot) {
    summary.replaceChildren();
    return;
  }
  const s = summarize(view.snapshot.targets, view.meta.thresholds);
  const item = (n, label) => el("li", {}, el("strong", { text: String(n) }), el("span", { text: label }));
  summary.replaceChildren(
    item(s.total, "targets"), item(s.ok, "OK"), item(s.warn, "warn"), item(s.crit, "crit"),
  );
}

function renderBody() {
  const empty = $("targets-empty");
  if (!view.snapshot) {
    empty.textContent = view.connected ? "Waiting for the first measurements…" : "Connecting to mping…";
    empty.hidden = false;
    return;
  }
  const shown = renderTargets($("targets"), view, openTarget);
  empty.hidden = shown > 0;
  empty.textContent = view.snapshot.targets.length === 0 ? "No targets." : "No targets match the filter.";
  $("http-panel").hidden = !renderHTTP($("http"), view.snapshot.http_checks);

  if (detail.id != null) {
    detail.update(view.snapshot.targets.find((t) => t.id === detail.id), view.meta);
  }
}

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
connect();
scheduleRender();
setInterval(refreshSparklines, SPARK_EVERY_MS);
refreshSparklines();
