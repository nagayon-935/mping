// Inspect pane: tabs below the monitor and graphs, the browser's counterpart
// to the TUI's Traceroute/MTR, Port, HTTP and Log panes. Summary, Path and
// Ports follow the selected target; HTTP and Log cover every target.
import { el, fetchJSON, statusChip } from "./dom.js";
import { setColumns } from "./columns.js";
import { renderHTTP } from "./table.js";
import { dscpName, formatCount, formatPct, formatRTT, hopLossLevel, lossRate, rowLevel, statusReasons } from "./model.js";

const TAB_KEY = "mping.inspectTab";
const EVENTS_EVERY_MS = 5000;
const EVENTS_LIMIT = 200;

const TABS = [
  { key: "summary", label: "Summary", perTarget: true, enabled: () => true },
  { key: "path", label: "Path", perTarget: true, enabled: (f) => f.traceroute || f.mtr },
  { key: "ports", label: "Ports", perTarget: true, enabled: (f) => f.port },
  { key: "http", label: "HTTP", perTarget: false, enabled: (f) => f.http },
  { key: "log", label: "Log", perTarget: false, enabled: () => true },
];

function facts(pairs) {
  const dl = el("dl", { className: "facts" });
  for (const [k, v] of pairs) {
    if (v == null || v === "") continue;
    dl.append(el("dt", { text: k }), el("dd", { text: String(v) }));
  }
  return dl;
}

function section(title, ...children) {
  return el("section", { className: "detail-section" }, el("h3", { text: title }), ...children);
}

function simpleTable(headers, rows) {
  const columns = headers.map(([label, num, key]) => ({ label, num, key }));
  const body = el("tbody");
  for (const cells of rows) {
    cells.forEach((cell, i) => cell.classList.add(`col-${columns[i].key}`));
    body.append(el("tr", {}, ...cells));
  }
  const table = el("table", { className: "grid" }, el("thead"), body);
  setColumns(table, columns);
  return el("div", { className: "table-wrap" }, table);
}

const td = (text, cls = "") => el("td", { className: cls, attrs: { title: text } }, el("span", { className: "cell-text", text }));
const num = (text) => td(text, "num");
const count = (value) => {
  const cell = num(formatCount(value));
  cell.title = value.toLocaleString("en-US");
  return cell;
};
const rtt = (value) => {
  const cell = num(formatRTT(value));
  cell.title = formatRTT(value, false);
  return cell;
};

/** The parts of the Summary tab that change with each snapshot. */
function summaryParts(t, meta) {
  const asn = [t.asn, t.org, t.country && `(${t.country})`].filter(Boolean).join(" ");
  const done = t.recv + t.loss;
  const level = rowLevel(t, meta.thresholds);
  return {
    status: [statusChip(level), el("span", { text: statusReasons(t, meta.thresholds).join(" · ") })],
    columns: [
      section("Current", facts([
        ["IP", t.ip || "–"],
        ["PTR", t.ptr],
        ["AS", asn],
        ["Loss", done === 0 ? "–" : `${formatPct(lossRate(t))}% (${t.loss} of ${done})`],
        ["Sent / Recv", `${t.sent} / ${t.recv}`],
        ["Latest RTT", formatRTT(t.last_rtt_ms, false)],
        ["Jitter", formatRTT(t.jitter_ms, false)],
        ["Duplicates", t.duplicates || null],
        ["Late replies", t.late_replies || null],
        ["Path MTU", t.pmtu ? `${t.pmtu}${t.pmtu_bottleneck_ip ? ` (bottleneck ${t.pmtu_bottleneck_ip})` : ""}` : null],
        ["Last error", t.last_error],
        ["Last loss", t.last_loss_time && new Date(t.last_loss_time).toLocaleTimeString()],
      ])),
      section("Since start / reset", facts([
        ["Average RTT", formatRTT(t.avg_rtt_ms, false)],
        ["Minimum RTT", formatRTT(t.min_rtt_ms, false)],
        ["Peak RTT", formatRTT(t.max_rtt_ms, false)],
        ["Latest TTL", t.last_ttl > 0 ? t.last_ttl : "–"],
        ["DSCP", meta.features.dscp ? dscpName(t.last_dscp) : null],
        ["Started", new Date(t.started_at).toLocaleString()],
      ]), el("p", { className: "chart-caption", text: "Historical averages and peaks do not determine the current status." })),
    ],
  };
}

function pathFor(t, th) {
  if (t.mtr_hops?.length) {
    return [simpleTable(
      [["Hop", true, "ttl"], ["Address", false, "ip"], ["AS", false, "asn"], ["Loss %", true, "hopLoss"], ["Sent", true, "count"],
        ["Last", true, "rtt"], ["Avg", true, "rtt"], ["Best", true, "rtt"], ["Worst", true, "rtt"], ["Jitter", true, "rtt"]],
      t.mtr_hops.map((h) => [
        num(String(h.ttl)),
        td(h.ip || "* no reply", "mono"),
        td([h.asn, h.org].filter(Boolean).join(" ") || "–"),
        el("td", { className: "num" }, statusChip(hopLossLevel(h.loss_pct, th), formatPct(h.loss_pct))),
        count(h.sent), rtt(h.last_rtt_ms), rtt(h.avg_rtt_ms),
        rtt(h.min_rtt_ms), rtt(h.max_rtt_ms), rtt(h.jitter_ms),
      ]),
    )];
  }
  if (t.trace_hops?.length) {
    const ol = el("ol", { className: "mono trace" });
    for (const hop of t.trace_hops) ol.append(el("li", { text: hop || "* no reply" }));
    return [ol];
  }
  return [el("p", { className: "empty", text: "No path data for this target yet." })];
}

function portsFor(t) {
  if (!t.port_results?.length) return [el("p", { className: "empty", text: "No port checks for this target." })];
  const levels = { Open: "ok", Closed: "crit", Filtered: "warn" };
  return [simpleTable(
    [["Port", false, "port"], ["Status", false, "statusLabel"], ["RTT", true, "rtt"], ["Open", true, "count"], ["Closed", true, "count"]],
    t.port_results.map((p) => [
      td(`${p.port}/${p.protocol}`, "mono"),
      el("td", {}, statusChip(levels[p.status] ?? "pending", p.status || "Waiting")),
      rtt(p.rtt_ms), count(p.open_count), count(p.closed_count),
    ]),
  )];
}

/**
 * @param {{tabs: HTMLElement, target: HTMLElement, body: HTMLElement,
 *          onSelect: (id: number) => void, actionsFor: (t: object) => Node | null,
 *          canControl: () => boolean, requestRender: () => void}} dom
 *
 * Snapshots arrive every second, so the pane avoids rebuilding what the user
 * may be interacting with: tab buttons are rebuilt only when the tab set or
 * the active tab changes, the Summary's action buttons only when the target
 * or the control permission changes (an armed delete survives updates), and
 * the Log only when new events arrive or its filter changes.
 */
export function createInspect(dom) {
  let active = null;
  try {
    active = sessionStorage.getItem(TAB_KEY);
  } catch {
    // Storage blocked: start on the default tab.
  }
  let view = null;
  let events = null; // last /api/v1/events body, or an Error
  let eventsVersion = 0;
  let onlySelected = false;
  let tabsKey = null;
  let mounted = { key: null, status: null, columns: null };

  const available = () => TABS.filter((t) => view?.meta && t.enabled(view.meta.features));
  const choose = (key) => {
    active = key;
    try {
      sessionStorage.setItem(TAB_KEY, key);
    } catch {
      // See above.
    }
    render(view);
  };

  const renderTabs = (tabs) => {
    dom.tabs.replaceChildren(...tabs.map((t) => {
      const button = el("button", {
        className: "tab",
        text: t.label,
        attrs: { type: "button", role: "tab", "aria-selected": String(t.key === active), "data-tab": t.key },
      });
      button.addEventListener("click", () => choose(t.key));
      return button;
    }));
  };

  const logTable = () => {
    if (events instanceof Error) return [el("p", { className: "empty", text: `Couldn't load events: ${events.message}` })];
    if (!events) return [el("p", { className: "empty", text: "Loading…" })];
    const selected = view.selectedId;
    // The filter only means something while a target is selected.
    const filtering = onlySelected && selected != null;
    const rows = events.events.filter((e) => !filtering || e.target_id === selected);
    const toggle = el("button", {
      className: "btn",
      text: onlySelected ? "Show all targets" : "Selected target only",
      attrs: { type: "button", "aria-pressed": String(onlySelected) },
    });
    toggle.hidden = selected == null;
    toggle.addEventListener("click", () => {
      onlySelected = !onlySelected;
      render(view);
    });
    if (rows.length === 0) return [toggle, el("p", { className: "empty", text: "No events recorded." })];
    const body = el("tbody");
    for (const e of rows) {
      const at = new Date(e.at);
      const host = el("button", { className: "link", text: e.host, attrs: { type: "button", title: `Select ${e.host}` } });
      host.addEventListener("click", () => dom.onSelect(e.target_id));
      body.append(el("tr", { className: e.target_id === selected ? "is-selected" : "" },
        el("td", { className: "mono" }, el("time", { text: at.toLocaleTimeString(), attrs: { datetime: at.toISOString() } })),
        el("td", { className: "col-host" }, host),
        el("td", { text: e.kind }),
        el("td", { className: "log-message", text: e.message })));
    }
    const head = el("tr", {}, ...["Time", "Host", "Kind", "Message"].map((h) => el("th", { text: h, attrs: { scope: "col" } })));
    return [toggle, el("div", { className: "table-wrap" }, el("table", { className: "grid log" }, el("thead", {}, head), body))];
  };

  const render = (next) => {
    view = next;
    if (!view?.meta) return;
    const tabs = available();
    if (!tabs.some((t) => t.key === active)) active = "summary";
    if (view.selectedId == null) onlySelected = false;
    const nextTabsKey = `${tabs.map((t) => t.key).join()}|${active}`;
    if (nextTabsKey !== tabsKey) {
      tabsKey = nextTabsKey;
      renderTabs(tabs);
    }
    const tab = tabs.find((t) => t.key === active);
    const target = view.snapshot.targets.find((t) => t.id === view.selectedId);
    dom.target.textContent = tab.perTarget ? (target ? `for ${target.host}` : "") : (tab.key === "log" ? "all targets" : "");
    if (tab.perTarget && !target) {
      mounted = { key: null };
      dom.body.replaceChildren(el("p", { className: "empty", text: "Select a target in the ping monitor or the RTT graphs." }));
      return;
    }
    const th = view.meta.thresholds;
    switch (tab.key) {
      case "summary": {
        const key = `summary:${target.id}:${dom.canControl()}`;
        if (mounted.key !== key) {
          const actions = dom.actionsFor(target);
          mounted = { key, status: el("div", { className: "detail-status" }), columns: el("div", { className: "summary-columns" }) };
          dom.body.replaceChildren(mounted.status, mounted.columns, ...(actions
            ? [el("section", { className: "detail-section", attrs: { "data-section": "actions" } }, el("h3", { text: "Actions" }), actions)]
            : []));
        }
        const parts = summaryParts(target, view.meta);
        mounted.status.replaceChildren(...parts.status);
        mounted.columns.replaceChildren(...parts.columns);
        return;
      }
      case "path": dom.body.replaceChildren(...pathFor(target, th)); break;
      case "ports": dom.body.replaceChildren(...portsFor(target)); break;
      case "http": {
        const table = el("table", { className: "grid", attrs: { id: "http" } }, el("thead"), el("tbody"));
        const any = renderHTTP(table, view.snapshot.http_checks);
        dom.body.replaceChildren(any ? el("div", { className: "table-wrap" }, table) : el("p", { className: "empty", text: "No HTTP checks." }));
        break;
      }
      case "log": {
        const key = `log:${eventsVersion}:${onlySelected}:${view.selectedId}`;
        if (mounted.key !== key) dom.body.replaceChildren(...logTable());
        mounted = { key };
        return;
      }
    }
    mounted = { key: null }; // path, ports and HTTP hold nothing interactive
  };

  const loadEvents = async () => {
    if (document.hidden) return;
    try {
      events = await fetchJSON(`api/v1/events?limit=${EVENTS_LIMIT}`);
    } catch (err) {
      events = err;
    }
    eventsVersion++;
    // Through the app's render queue, which holds while text is selected.
    if (active === "log") dom.requestRender();
  };
  setInterval(loadEvents, EVENTS_EVERY_MS);
  loadEvents();

  return {
    render,
    /** Opens a tab by key when it exists (e.g. after selecting a target). */
    show(key) {
      if (available().some((t) => t.key === key)) choose(key);
    },
    get active() { return active; },
  };
}
