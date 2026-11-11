// Monitor workspace rendering. Selection, ordering and fetching live outside
// this module; the table and route panes share one prepared target list.
import { $ } from "./dom.js";
import { commonCauseText, detectCommonCause } from "./common_cause.js";
import { prepareSparks } from "./spark_view.js";
import { renderTargets } from "./table.js";
import { renderMTR } from "./mtr.js";
import { renderTraceroutes } from "./traceroute.js";
import { STATUS_LABELS } from "./toolbar.js";

export function createWorkspaceView({ inspect, canControl, selectTarget, sortColumn, renderUnlessSelecting, selectedId }) {
  // Locate scrolls a pane to the selected host without reordering anything.
  const locators = [
    ["locate-mtr", () => $("mtr-table-wrap"), "#mtr tbody", 36], // below the sticky column header
    ["locate-traceroute", () => $("traceroute-scroll"), ".trace-target", 0],
  ];
  for (const [button, scroller, selector, offset] of locators) {
    $(button).addEventListener("click", () => {
      const node = document.querySelector(`${selector}[data-target-id="${selectedId()}"]`);
      if (!node) return;
      const scroll = scroller();
      scroll.scrollTop += node.getBoundingClientRect().top - scroll.getBoundingClientRect().top - offset;
    });
  }

  function render(view, { targets, total, groupHeaders, orderUpToDate }) {
    const { features } = view.meta;
    const showMTR = !!features.mtr;
    const showTrace = !!features.traceroute && !showMTR;

    $("pane-inspect").hidden = !view.inspectOpen;
    $("workspace").classList.toggle("has-inspect", view.inspectOpen);
    $("pane-mtr").hidden = !showMTR;
    $("pane-traceroute").hidden = !showTrace;
    $("workspace").classList.toggle("has-route", showMTR || showTrace);
    // Size the monitor for every host, not the filtered ones, so filtering
    // never moves the route pane.
    setMonitorSize("--monitor-rows", String(total));
    setMonitorSize("--monitor-groups", String(groupHeaders));

    if (showMTR) renderMTRPane(view, targets, total);
    if (showTrace) renderTraceroutePane(view, targets);
    renderCommonCause(view.snapshot.targets);
    renderTargetsPane(view, targets, total);
    measureMonitor();
    renderMonitorChrome(view, targets, total, showMTR || showTrace, orderUpToDate);
    renderUnlessSelecting($("inspect-body"), () => { if (view.inspectOpen) inspect.render(view); });
  }

  /** Host and group rows have fixed heights in style.css; the column header
   * and a horizontal scrollbar (non-overlay scrollbars) are measured. */
  function measureMonitor() {
    const table = $("targets");
    const head = table.tHead.getBoundingClientRect().height;
    if (!head) return; // not laid out (hidden): keep the last measurement
    setMonitorSize("--monitor-head", `${Math.ceil(head * 4) / 4}px`);
    const scroll = table.closest(".pane-scroll");
    setMonitorSize("--monitor-hscroll", `${scroll.offsetHeight - scroll.clientHeight}px`);
  }

  function setMonitorSize(name, value) {
    const pane = $("pane-monitor");
    if (pane.style.getPropertyValue(name) !== value) pane.style.setProperty(name, value);
  }

  /** One banner instead of the same red cell on most rows. It considers
   * every host, not just the filtered ones: the cause is fleet-wide. */
  function renderCommonCause(allTargets) {
    const cause = detectCommonCause(allTargets);
    const banner = $("common-cause");
    banner.hidden = !cause;
    if (!cause) return;
    const text = commonCauseText(cause);
    $("common-cause-headline").textContent = text.headline;
    $("common-cause-detail").textContent = text.detail;
    banner.title = text.title;
  }

  const hostCount = (n) => `${n} ${n === 1 ? "host" : "hosts"} · ICMP`;

  function renderMTRPane(view, targets, total) {
    $("mtr-summary").textContent = hostCount(targets.length);
    renderUnlessSelecting($("mtr-scroll"), () => {
      const groups = renderMTR($("mtr"), targets, view.meta, view.selectedId);
      $("mtr-table-wrap").hidden = groups === 0;
      $("mtr-empty").hidden = groups > 0;
      $("mtr-empty").textContent = total === 0 ? "No targets." : "No targets match the filter.";
    });
  }

  function renderTraceroutePane(view, targets) {
    $("traceroute-summary").textContent = hostCount(targets.length);
    renderUnlessSelecting($("traceroute-scroll"), () =>
      renderTraceroutes($("traceroute-scroll"), targets, view.meta.features, view.selectedId));
  }

  function emptyTargetsMessage(view, total) {
    if (total === 0) return "No hosts are being monitored. Add a host here or in the TUI / hosts file.";
    const query = view.filter ? `“${view.filter}” and ` : "";
    return `No hosts match ${query}${STATUS_LABELS[view.status] ?? "the current filters"}.`;
  }

  function renderTargetsPane(view, targets, total) {
    const { features } = view.meta;
    const fields = ["host", "IP", ...(features.ptr ? ["PTR"] : []), ...(features.asn ? ["AS"] : [])].join(", ");
    $("filter").placeholder = `Filter ${fields}`;
    $("filter").setAttribute("aria-label", `Filter ${fields}`);
    renderUnlessSelecting($("targets"), () => {
      const sparks = prepareSparks(targets, view.series, view.meta.interval_ms, view.historyNow ?? Date.now());
      const shown = renderTargets($("targets"), { ...view, visibleTargets: targets, sparks }, selectTarget, sortColumn);
      $("targets-empty").hidden = shown > 0;
      $("targets-empty-message").textContent = emptyTargetsMessage(view, total);
      $("empty-action").hidden = false;
      $("empty-action").textContent = total ? "Clear filters" : canControl() ? "Add host" : "Enable controls";
    });
  }

  /** Counts, sort state, locate buttons and hints around the monitor panes. */
  function renderMonitorChrome(view, targets, total, routeShown, orderUpToDate) {
    $("shown-count").textContent = `${targets.length} / ${total} hosts`;

    const refresh = $("refresh-sort");
    refresh.disabled = orderUpToDate;
    refresh.textContent = refresh.disabled ? "Order up to date" : "Refresh order •";

    const selectedVisible = view.selectedId != null && targets.some((t) => t.id === view.selectedId);
    $("selection-note").hidden = view.selectedId == null || selectedVisible;
    for (const id of ["locate-mtr", "locate-traceroute"]) {
      $(id).disabled = !selectedVisible;
      $(id).title = selectedVisible ? "Scroll to the selected host without changing order." : "Select a host visible in the current filters.";
    }

    const hint = `Loss % is lifetime loss; health uses recent measurements.${routeShown ? "" : " Route monitoring is disabled (start with -M or -T)."}`;
    $("monitor-hint").textContent = hint;
    $("monitor-hint").title = hint;
  }

  return { render };
}
