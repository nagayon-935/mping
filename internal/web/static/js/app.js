// One SSE stream drives the monitoring workspace and independent global pages.
import { el, badgeIcon, fetchJSON, readSession, selectionWithin, writeSession } from "./dom.js";
import { renderTargets } from "./table.js";
import { createGraphs } from "./graphs.js";
import { createInspect } from "./inspect.js";
import { renderMTR } from "./mtr.js";
import { renderTraceroutes } from "./traceroute.js";
import { createLog } from "./log.js";
import { httpFor } from "./checks.js";
import { badgeFor, summarize, validateHost } from "./model.js";
import { reconcileWindow } from "./timeline.js";
import { createControl } from "./control.js";
import { formatTime } from "./time.js";
import { createReportDialog } from "./report.js";
import { createConfirmation } from "./dialogs.js";
import { SORTS, configuredTargets, sortTargets, frozenOrder, filterTargets, editableTarget } from "./workspace.js";

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
  const node = $("notice"), text = $("notice-message");
  clearTimeout(noticeTimer);
  text.setAttribute("role", isError ? "alert" : "status");
  text.setAttribute("aria-live", isError ? "assertive" : "polite");
  text.textContent = message;
  node.hidden = false;
  node.classList.toggle("is-error", isError);
  if (!isError) noticeTimer = setTimeout(() => { node.hidden = true; text.textContent = ""; }, 4000);
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
  status: readSession("mping.status") || "all",
  sort: {key:"config",direction:"asc"}, order: null, focusedId: null,
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
    const button = el("button", {className:"btn btn-danger",text:`Delete ${t.host}`,attrs:{type:"button"}});
    button.addEventListener("click", () => deleteTarget(t.id));
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
  dialog:$("confirm-dialog"),form:$("confirm-form"),title:$("confirm-title"),description:$("confirm-description"),
  error:$("confirm-error"),cancel:$("confirm-cancel"),submit:$("confirm-action"),
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

  log.activate(view.page === "log");
}

function baseTargets() { return configuredTargets(view.snapshot.targets, view.meta.groups); }
function refreshOrder() {
  if (!view.snapshot) return;
  view.order = sortTargets(baseTargets(), view.sort, view.meta.thresholds).map(t => t.id);
  scheduleRender();
}
function setSort(key, direction) {
  view.sort = { key, direction: direction || (["host", "ip", "config"].includes(key) ? "asc" : "desc") };
  writeSession("mping.sort", JSON.stringify(view.sort));
  $("sort-order").value = `${view.sort.key}:${view.sort.direction}`;
  refreshOrder();
}
function sortColumn(key) {
  const first = ["host", "ip"].includes(key) ? "asc" : "desc";
  if (view.sort.key !== key) setSort(key, first);
  else if (view.sort.direction === first) setSort(key, first === "asc" ? "desc" : "asc");
  else setSort("config", "asc");
}
function setStatus(status) {
  view.status = status;
  $("status-filter").value = status;
  writeSession("mping.status", status);
  if (view.page !== "monitor") { view.page = "monitor"; history.replaceState(null,"",view.selectedId == null ? "#monitor" : `#target-${view.selectedId}`); }
  scheduleRender();
}
function clearFilters() {
  view.filter = ""; $("filter").value = ""; writeSession(FILTER_KEY, "");
  setStatus("all");
}
function visibleTargets() {
  const base = baseTargets();
  if (view.order === null) view.order = sortTargets(base, view.sort, view.meta.thresholds).map(t => t.id);
  const ordered = view.sort.key === "config" ? base : frozenOrder(base, view.order);
  return filterTargets(ordered, view.filter, view.status, view.meta);
}

function renderHeader() {
  const badge = badgeFor(view.state, view.connected);
  const node = $("badge");
  node.className = `badge lvl-${badge.level}`;
  node.querySelector(".badge-icon").textContent = badgeIcon(badge.level);
  node.querySelector(".badge-label").textContent = `Connection: ${badge.label}`;
  node.title = "Browser connection, independent of host health and measurement state.";
  const measurement = view.meta?.measurement_state ?? view.snapshot?.targets[0]?.measurement_state;
  const stopped = measurement === "stopped" || measurement === "finished";
  $("measurement-state").textContent = measurement ? `Measurements: ${measurement}` : "Measurements: waiting";
  $("measurement-state").classList.toggle("is-stopped", stopped);
  if (!$("measurement-action").disabled) $("measurement-action").textContent = stopped ? "Restart all" : "Stop all";
  $("measurement-action").title = "Stop or restart measurements for all hosts and enabled monitors. Existing statistics are retained.";
  const latest = view.snapshot?.targets.map(t => t.health?.last_probe_at).filter(Boolean).sort().at(-1);
  $("updated").textContent = `Last probe ${latest ? formatTime(latest) : "–"}`;
  $("connection-note").textContent = !view.connected && view.snapshot
    ? view.state === "stopped" ? "Session ended; last values retained." : "Reconnecting; showing last received values." : "";
  const counts = view.snapshot ? summarize(view.snapshot.targets, view.meta.thresholds) : {total:0,crit:0,warn:0,pending:0,ok:0};
  for (const button of $("summary").querySelectorAll("button")) {
    const status = button.dataset.status;
    button.querySelector("strong").textContent = String(status === "all" ? counts.total : counts[status]);
    button.setAttribute("aria-pressed", String(view.status === status));
    button.disabled = !view.snapshot;
  }
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
    $("targets-empty-message").textContent = view.connected ? "Waiting for the first measurement…" : "Connecting to mping…";
    $("empty-action").hidden = true;
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
      const groups = renderMTR($("mtr"), targets, view.meta, view.selectedId);
      $("mtr-table-wrap").hidden = groups === 0;
      $("mtr-empty").hidden = groups > 0;
      $("mtr-empty").textContent = view.snapshot.targets.length === 0 ? "No targets." : "No targets match the filter.";
    }
  }
  if (showTrace) {
    const targets = visibleTargets();
    $("traceroute-summary").textContent = `${targets.length} ${targets.length === 1 ? "host" : "hosts"} · ICMP`;
    if (selectionWithin($("traceroute-scroll"))) heldBySelection = true;
    else renderTraceroutes($("traceroute-scroll"), targets, view.meta.features, view.selectedId);
  }
  const fields = ["host", "IP", ...(view.meta.features.ptr ? ["PTR"] : []), ...(view.meta.features.asn ? ["AS"] : [])].join(", ");
  $("filter").placeholder = `Filter ${fields}`;
  $("filter").setAttribute("aria-label", `Filter ${fields}`);
  if (selectionWithin($("targets"))) {
    heldBySelection = true;
  } else {
    const targets = visibleTargets();
    const shown = renderTargets($("targets"), {...view, visibleTargets:targets}, selectTarget, sortColumn);
    empty.hidden = shown > 0;
    $("targets-empty-message").textContent = view.snapshot.targets.length === 0
      ? "No hosts are being monitored. Add a host here or in the TUI / hosts file."
      : `No hosts match ${view.filter ? `“${view.filter}” and ` : ""}${view.status === "all" ? "the current filters" : view.status === "crit" ? "Critical" : view.status === "warn" ? "Warning" : view.status === "pending" ? "Waiting" : "OK"}.`;
    $("empty-action").hidden = false;
    $("empty-action").textContent = view.snapshot.targets.length ? "Clear filters" : control.allowed ? "Add host" : "Enable controls";
  }
  const targets = visibleTargets();
  $("shown-count").textContent = `${targets.length} / ${view.snapshot.targets.length} hosts`;
  const desired = sortTargets(baseTargets(), view.sort, view.meta.thresholds).map(t=>t.id);
  const ordered = frozenOrder(baseTargets(), view.order).map(t=>t.id);
  $("refresh-sort").disabled = view.sort.key === "config" || desired.join() === ordered.join();
  $("refresh-sort").textContent = $("refresh-sort").disabled ? "Order up to date" : "Refresh order •";
  $("selection-note").hidden = view.selectedId == null || targets.some(t=>t.id === view.selectedId);
  for (const id of ["locate-mtr","locate-traceroute","locate-graphs"]) {
    $(id).disabled = view.selectedId == null || !targets.some(t=>t.id === view.selectedId);
    $(id).title = $(id).disabled ? "Select a host visible in the current filters." : "Scroll to the selected host without changing order.";
  }
  $("graphs-empty").hidden = targets.length > 0;
  $("graphs-empty").textContent = view.snapshot.targets.length ? "No hosts match the current filters." : "Add a host to start collecting RTT history.";
  $("monitor-hint").textContent = `Loss % is lifetime loss; health uses recent measurements.${showMTR || showTrace ? "" : " Route monitoring is disabled (start with -M or -T)."}`;
  $("monitor-hint").title = $("monitor-hint").textContent;
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
    control.refresh().then(() => { renderControls(); scheduleRender(); });
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
  $("measurement-controls").hidden = !on;
  $("toggle-actions").hidden = !on;
  $("open-auth").textContent = on ? "Controls enabled" : "Enable controls";
  $("open-auth").disabled = on;
  if (!on) {
    report.close();
    $("add-dialog").close();
    document.querySelector('#inspect-body [data-section="actions"]')?.remove();
  }
}

function openAuth() {
  if (control.allowed) return;
  $("auth-token").value = ""; $("auth-token").type = "password"; $("auth-show").checked = false;
  $("auth-error").hidden = true; $("auth-token").removeAttribute("aria-invalid");
  $("auth-dialog").showModal();
}
function openAdd() {
  if (!control.allowed) return openAuth();
  $("add-error").hidden = true;
  $("add-dialog").showModal();
}
function deleteTarget(id) {
  if (!control.allowed) return;
  const target = view.snapshot?.targets.find(t=>t.id === id);
  if (!target) return;
  confirmation.open({ title:"Delete host?", label:"Delete host",
    description:`${target.host}\n${target.ip || "Address not resolved"}\nTarget #${target.id}\n\nMeasurements for this host will stop. Re-adding it starts a new measurement.`,
    run:async()=>{
      const result = await control.deleteTarget(id);
      if (result.ok) {
        notify(`Deleted ${target.host}.`);
        if (view.selectedId === id) clearSelection();
        requestAnimationFrame(()=>$("filter").focus());
      }
      renderControls(); scheduleRender();
      return result;
    },
  });
}

function initControls() {
  $("refresh-log").addEventListener("click", log.refresh);
  $("dismiss-notice").addEventListener("click",()=>{clearTimeout(noticeTimer);$("notice").hidden=true;$("notice-message").textContent="";});
  $("close-inspect").addEventListener("click",()=>{clearSelection();$("filter").focus();});
  $("toggle-actions").addEventListener("click",()=>{const on=$("toggle-actions").getAttribute("aria-expanded")!=="true";$("toggle-actions").setAttribute("aria-expanded",String(on));$("measurement-controls").classList.toggle("actions-open",on);});
  $("measurement-controls").addEventListener("click",event=>{if(event.target.closest("button")){$("measurement-controls").classList.remove("actions-open");$("toggle-actions").setAttribute("aria-expanded","false");}});
  document.addEventListener("click",event=>{if(!event.target.closest("#measurement-controls, #toggle-actions")){$("measurement-controls").classList.remove("actions-open");$("toggle-actions").setAttribute("aria-expanded","false");}});
  for (const dialog of document.querySelectorAll("dialog")) {
    dialog.addEventListener("close", () => requestAnimationFrame(() => {
      const active = document.activeElement;
      // A menu action is hidden once it opens a dialog. Restore focus to the
      // mobile menu trigger after the browser finishes its own focus restore.
      if ($("toggle-actions").getBoundingClientRect().width &&
          (active === document.body || dialog.contains(active) || !active.getClientRects().length)) {
        $("toggle-actions").focus();
      }
    }));
  }
  $("open-report").addEventListener("click",report.open);
  $("open-add").addEventListener("click",openAdd);
  $("open-auth").addEventListener("click",openAuth);
  document.querySelectorAll("[data-close]").forEach(button=>button.addEventListener("click",()=>{if(button.dataset.close==="auth-dialog")authenticating?.abort();$(button.dataset.close).close();}));
  $("open-shortcuts").addEventListener("click",()=>$("shortcuts-dialog").showModal());
  $("shortcuts-enabled").checked=readSession("mping.shortcuts")!=="false";
  $("shortcuts-enabled").addEventListener("change",()=>writeSession("mping.shortcuts",String($("shortcuts-enabled").checked)));
  $("measurement-action").addEventListener("click",async()=>{
    const state=view.meta?.measurement_state??view.snapshot?.targets[0]?.measurement_state;
    const restart=state==="stopped"||state==="finished";
    const button=$("measurement-action");button.disabled=true;button.textContent=restart?"Restarting…":"Stopping…";
    try {
      const result=await(restart?control.restart():control.stop());
      notify(result.ok?restart?"All measurements restarted.":"All measurements stopped. Statistics are retained.":result.error,!result.ok);
      if(result.ok){try{applySnapshot(await fetchJSON("api/v1/snapshot"));}catch{}}
      await refreshHistory(); renderControls();
    }finally{button.disabled=false;scheduleRender();}
  });
  const reset=el("button",{className:"btn btn-danger",text:"Reset stats",attrs:{type:"button","aria-haspopup":"dialog"}});
  reset.addEventListener("click",()=>confirmation.open({title:"Reset all statistics?",label:"Reset statistics",
    description:`Reset counters and RTT histories for all hosts and enabled monitors.\n\nThe current running/stopped state is retained. This cannot be undone.`,
    run:async()=>{const result=await control.reset();if(result.ok){view.series=new Map();notify("Statistics reset.");await refreshHistory();}renderControls();scheduleRender();return result;},
  }));
  $("reset-slot").replaceChildren(reset);
  $("readonly-explanation").textContent="Press b in mping's TUI and choose Copy URL, then paste it below. You can also paste MPING_WEB_TOKEN for a headless run. Controls are verified without reloading this page.";
  let authenticating=null;
  const abortAuth=()=>authenticating?.abort();
  $("auth-dialog").addEventListener("cancel",abortAuth);
  $("auth-dialog").addEventListener("close",()=>{abortAuth();$("auth-token").value="";});
  $("auth-show").addEventListener("change",()=>{$("auth-token").type=$("auth-show").checked?"text":"password";});
  $("auth-token").addEventListener("input",()=>{$("auth-error").hidden=true;$("auth-token").removeAttribute("aria-invalid");});
  $("auth-form").addEventListener("submit",async event=>{
    event.preventDefault();if(authenticating)return;
    const controller=new AbortController();authenticating=controller;
    $("auth-submit").disabled=true;$("auth-submit").textContent="Verifying…";
    try{
      const result=await control.authenticate($("auth-token").value,controller.signal);
      if(controller.signal.aborted)return;
      if(result.ok){$("auth-dialog").close();notify("Browser controls enabled.");renderControls();scheduleRender();}
      else{$("auth-error").textContent=result.error;$("auth-error").hidden=false;$("auth-token").setAttribute("aria-invalid","true");$("auth-token").focus();}
    }finally{authenticating=null;$("auth-submit").disabled=false;$("auth-submit").textContent="Enable controls";}
  });
  let adding=false;
  $("add-form").addEventListener("submit",async event=>{
    event.preventDefault();if(adding)return;
    const checked=validateHost($("add-host").value);
    if(checked.error){$("add-error").textContent=checked.error;$("add-error").hidden=false;$("add-host").focus();return;}
    adding=true;$("add-submit").disabled=true;$("add-submit").textContent="Adding…";
    try{
      const result=await control.addHost(checked.host);
      if(result.ok){$("add-dialog").close();$("add-host").value="";notify(`Added ${checked.host}.`);}
      else{$("add-error").textContent=result.error;$("add-error").hidden=false;if(!control.allowed)notify(result.error,true);}
      renderControls();scheduleRender();
    }finally{adding=false;$("add-submit").disabled=false;$("add-submit").textContent="Add";}
  });
  for(const [button,container,selector] of [["locate-mtr","mtr-table-wrap","#mtr tbody"],["locate-traceroute","traceroute-scroll",".trace-target"],["locate-graphs",null,".graph-card"]]){
    $(button).addEventListener("click",()=>{
      const node=document.querySelector(`${selector}[data-target-id="${view.selectedId}"]`);
      if(!node)return;
      const scroll=container?$(container):$("graphs").parentElement;
      const heading=node.getBoundingClientRect(),bounds=scroll.getBoundingClientRect();
      scroll.scrollTop+=heading.top-bounds.top-(button==="locate-mtr"?36:0);
    });
  }
  renderControls();control.refresh().then(()=>{renderControls();scheduleRender();});
}

function initFilter() {
  $("filter").value=readSession(FILTER_KEY)??"";view.filter=$("filter").value;
  $("filter").addEventListener("input",()=>{view.filter=$("filter").value;writeSession(FILTER_KEY,view.filter);scheduleRender();});
  if(!["all","crit","warn","pending","ok"].includes(view.status))view.status="all";
  $("status-filter").value=view.status;
  $("status-filter").addEventListener("change",event=>setStatus(event.target.value));
  const items=[["all","All",""],["crit","Critical","■"],["warn","Warning","▲"],["pending","Waiting","○"],["ok","OK","●"]];
  $("summary").replaceChildren(...items.map(([status,label,icon])=>{
    const button=el("button",{className:`health-filter health-${status}`,attrs:{type:"button","data-status":status,"aria-pressed":String(status===view.status)}},el("span",{text:icon,attrs:{"aria-hidden":"true"}}),el("strong",{text:"0"}),el("span",{text:label}));
    button.addEventListener("click",()=>setStatus(status));return el("li",{},button);
  }));
  let saved;try{saved=JSON.parse(readSession("mping.sort"));}catch{}
  if(saved&&SORTS[saved.key]&&["asc","desc"].includes(saved.direction))view.sort=saved;
  for(const [key,label]of Object.entries(SORTS))for(const direction of key==="config"?["asc"]:["desc","asc"]){
    $("sort-order").append(el("option",{text:key==="config"?label:`${label} ${direction==="desc"?"↓":"↑"}`,attrs:{value:`${key}:${direction}`}}));
  }
  $("sort-order").value=`${view.sort.key}:${view.sort.direction}`;
  $("sort-order").addEventListener("change",event=>{const[key,direction]=event.target.value.split(":");setSort(key,direction);});
  $("refresh-sort").addEventListener("click",refreshOrder);
  $("clear-filter").addEventListener("click",clearFilters);
  $("empty-action").addEventListener("click",()=>view.snapshot?.targets.length?clearFilters():openAdd());
  $("targets").addEventListener("focusin",event=>{const row=event.target.closest(".target-row");if(row){view.focusedId=Number(row.dataset.targetId);$("targets").querySelectorAll(".host-open").forEach(button=>button.tabIndex=button.dataset.targetId===row.dataset.targetId?0:-1);}});
  document.addEventListener("keydown",event=>{
    if(event.defaultPrevented||event.isComposing||event.ctrlKey||event.metaKey||event.altKey||document.querySelector("dialog[open]"))return;
    if(event.key==="Escape"&&$("toggle-actions").getAttribute("aria-expanded")==="true"){
      event.preventDefault();$("measurement-controls").classList.remove("actions-open");$("toggle-actions").setAttribute("aria-expanded","false");$("toggle-actions").focus();return;
    }
    if(editableTarget(event.target)){
      if(event.key==="Escape"&&event.target.id==="filter"){event.preventDefault();$("targets").querySelector(".host-open[tabindex='0']")?.focus();}
      return;
    }
    const characters=$("shortcuts-enabled").checked;
    const row=event.target.closest?.("#targets .target-row");
    if(row){
      const buttons=[...$("targets").querySelectorAll(".host-open")];
      const index=buttons.findIndex(button=>button.dataset.targetId===row.dataset.targetId);
      let next;
      if(event.key==="ArrowDown"||(characters&&event.key==="j"))next=Math.min(buttons.length-1,index+1);
      if(event.key==="ArrowUp"||(characters&&event.key==="k"))next=Math.max(0,index-1);
      if(event.key==="Home")next=0;if(event.key==="End")next=buttons.length-1;
      if(next!==undefined){event.preventDefault();buttons[next]?.focus({preventScroll:true});buttons[next]?.closest("tr").scrollIntoView({block:"nearest"});return;}
      if(event.key==="Delete"&&control.allowed){event.preventDefault();deleteTarget(Number(row.dataset.targetId));return;}
    }
    if(!characters)return;
    if(event.key==="/"){event.preventDefault();view.page="monitor";history.replaceState(null,"",view.selectedId==null?"#monitor":`#target-${view.selectedId}`);scheduleRender();requestAnimationFrame(()=>$("filter").focus());}
    if(event.key==="?"){event.preventDefault();$("shortcuts-dialog").showModal();}
    if(event.key==="w"&&control.allowed){event.preventDefault();report.open();}
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
