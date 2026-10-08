// Detail drawer for one target: facts, RTT chart with hover, hops, ports and
// events. Polls its own history/events endpoints only while open.
import { el, fetchJSON, statusChip } from "./dom.js";
import { drawChart, drawCrosshair, indexAt } from "./chart.js";
import { setColumns } from "./columns.js";
import { dscpName, formatCount, formatRTT, formatPct, hopLossLevel, lossRate, rowLevel, statusReasons } from "./model.js";

const HISTORY_POINTS = 300;
const HISTORY_EVERY_MS = 2000;
const EVENTS_EVERY_MS = 5000;

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

function sectionWith(key, title, ...children) {
  const node = section(title, ...children);
  node.dataset.section = key;
  return node;
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

function factsFor(t) {
  const asn = [t.asn, t.org, t.country && `(${t.country})`].filter(Boolean).join(" ");
  const done = t.recv + t.loss;
  return facts([
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
    ["Started", new Date(t.started_at).toLocaleString()],
  ]);
}

function statisticsFor(t, meta) {
  return facts([
    ["Average RTT", formatRTT(t.avg_rtt_ms, false)],
    ["Minimum RTT", formatRTT(t.min_rtt_ms, false)],
    ["Peak RTT", formatRTT(t.max_rtt_ms, false)],
    ["Latest TTL", t.last_ttl > 0 ? t.last_ttl : "–"],
    ["DSCP", meta.features.dscp ? dscpName(t.last_dscp) : null],
  ]);
}

function hopsFor(t, th) {
  const parts = [];
  if (t.mtr_hops?.length) {
    parts.push(section("MTR", simpleTable(
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
    )));
  } else if (t.trace_hops?.length) {
    const ol = el("ol", { className: "mono" });
    for (const hop of t.trace_hops) ol.append(el("li", { text: hop }));
    parts.push(section("Traceroute", ol));
  }
  if (t.port_results?.length) {
    const levels = { Open: "ok", Closed: "crit", Filtered: "warn" };
    parts.push(section("Ports", simpleTable(
      [["Port", false, "port"], ["Status", false, "statusLabel"], ["RTT", true, "rtt"], ["Open", true, "count"], ["Closed", true, "count"]],
      t.port_results.map((p) => [
        td(`${p.port}/${p.protocol}`, "mono"),
        el("td", {}, statusChip(levels[p.status] ?? "pending", p.status || "Waiting")),
        rtt(p.rtt_ms), count(p.open_count), count(p.closed_count),
      ]),
    )));
  }
  return parts;
}

function eventList(events) {
  if (events.length === 0) return el("p", { className: "muted", text: "No events recorded." });
  const ul = el("ul", { className: "events" });
  for (const e of [...events].reverse()) {
    const at = new Date(e.at);
    ul.append(el("li", {},
      el("time", { text: at.toLocaleTimeString(), attrs: { datetime: at.toISOString() } }),
      el("span", { className: "kind", text: e.kind }),
      el("span", { text: e.message })));
  }
  return ul;
}

/** Chart with crosshair tooltip on pointer and arrow keys. */
function createChart(getContext) {
  const canvas = el("canvas", {
    className: "chart",
    attrs: { tabindex: "0", role: "img", "aria-label": "RTT over time; use arrow keys to inspect samples" },
  });
  const tip = el("div", { className: "tooltip", attrs: { hidden: "" } });
  const caption = el("p", { className: "chart-caption" });
  const wrap = el("div", { className: "chart-wrap" }, canvas, tip);
  let series = [];
  let geo = null;
  let cursor = -1;

  const paint = () => {
    const { th, intervalMs } = getContext();
    geo = drawChart(canvas, series, th, intervalMs);
    if (cursor >= 0 && cursor < series.length) {
      drawCrosshair(canvas, geo, series, cursor);
      const v = series[cursor];
      const ago = Math.round(((series.length - 1 - cursor) * intervalMs) / 1000);
      tip.replaceChildren(el("strong", { text: v == null ? "Lost" : formatRTT(v) }), el("span", { text: ago === 0 ? "latest" : `≈ ${ago}s ago` }));
      tip.hidden = false;
      const x = geo.xAt(cursor);
      tip.style.left = `${Math.min(x + 10, canvas.clientWidth - tip.offsetWidth - 4)}px`;
      tip.style.top = "8px";
    } else {
      tip.hidden = true;
    }
  };

  canvas.addEventListener("pointermove", (e) => {
    if (!geo) return;
    cursor = indexAt(geo, series.length, e.offsetX);
    paint();
  });
  canvas.addEventListener("pointerleave", () => { cursor = -1; paint(); });
  canvas.addEventListener("keydown", (e) => {
    if (series.length === 0) return;
    if (e.key === "ArrowLeft") cursor = cursor < 0 ? series.length - 1 : Math.max(0, cursor - 1);
    else if (e.key === "ArrowRight") cursor = cursor < 0 ? series.length - 1 : Math.min(series.length - 1, cursor + 1);
    else if (e.key === "Escape") cursor = -1;
    else return;
    e.preventDefault();
    paint();
  });
  canvas.addEventListener("blur", () => { cursor = -1; paint(); });

  return {
    node: el("div", {}, wrap, caption),
    set(next) {
      series = next;
      if (cursor >= series.length) cursor = -1;
      const vals = series.filter((v) => v != null);
      const lost = series.length - vals.length;
      caption.textContent = vals.length === 0
        ? (series.length === 0 ? "No samples yet." : `All ${lost} samples lost.`)
        : `${series.length} samples · min ${formatRTT(Math.min(...vals))} · max ${formatRTT(Math.max(...vals))} · ${lost} lost (red ticks)`;
      paint();
    },
    repaint: paint,
  };
}

/**
 * @param {{root: HTMLElement, title: HTMLElement, sub: HTMLElement, body: HTMLElement, closeButton: HTMLElement,
 *          onClose: () => void, actionsFor: (target: object) => Node | null}} dom
 */
export function createDetail(dom) {
  let id = null;
  let target = null;
  let meta = null;
  let timers = [];
  let aborter = null;
  let factsNode, statisticsNode, hopsNode, eventsNode;
  const chart = createChart(() => ({ th: meta.thresholds, intervalMs: meta.interval_ms }));

  // A response can finish after the drawer moved to another target or
  // closed (abort only cancels requests still in flight), so results are
  // applied only if they still belong to the target on screen.
  const poll = async (url, apply) => {
    const { signal } = aborter;
    const forId = id;
    const current = () => !signal.aborted && forId === id;
    try {
      const body = await fetchJSON(url, signal);
      if (current()) apply(body);
    } catch (err) {
      if (current()) apply(null, err);
    }
  };
  const loadHistory = () => poll(`api/v1/targets/${id}/history?n=${HISTORY_POINTS}`, (body) => {
    if (body) chart.set(body.rtt_ms);
  });
  const loadEvents = () => poll(`api/v1/targets/${id}/events`, (body, err) => {
    eventsNode.replaceChildren(body ? eventList(body.events) : el("p", { className: "muted", text: `Couldn't load events: ${err.message}` }));
  });

  const stop = () => {
    timers.forEach(clearInterval);
    timers = [];
    aborter?.abort();
  };

  const close = () => {
    if (id == null) return;
    stop();
    id = null;
    dom.root.hidden = true;
    dom.onClose();
  };
  dom.closeButton.addEventListener("click", close);
  document.addEventListener("keydown", (e) => {
    // Escape in a text field belongs to the field (e.g. clearing the filter).
    if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
    if (e.key === "Escape" && id != null && !e.defaultPrevented) close();
  });
  window.addEventListener("resize", () => id != null && chart.repaint());

  const render = () => {
    dom.title.textContent = target.host;
    dom.sub.textContent = [target.ip, target.ptr].filter(Boolean).join(" · ");
    const level = rowLevel(target, meta.thresholds);
    factsNode.replaceChildren(
      el("div", { className: "detail-status" }, statusChip(level),
        el("span", { text: statusReasons(target, meta.thresholds).join(" · ") })),
      factsFor(target));
    statisticsNode.replaceChildren(statisticsFor(target, meta));
    hopsNode.replaceChildren(...hopsFor(target, meta.thresholds));
  };

  return {
    get id() { return id; },
    open(nextId, t, m) {
      stop();
      id = nextId;
      target = t;
      meta = m;
      aborter = new AbortController();
      factsNode = el("div");
      statisticsNode = el("div");
      hopsNode = el("div");
      eventsNode = el("div", {}, el("p", { className: "muted", text: "Loading…" }));
      const actions = dom.actionsFor(t);
      // replaceChildren converts null into visible text, unlike our el helper.
      const sections = [
        section("Summary", factsNode),
        section("RTT", chart.node),
        section("Statistics since start / reset", statisticsNode,
          el("p", { className: "chart-caption", text: "Historical averages and peaks do not determine the current status." })),
        hopsNode,
        section("Events", eventsNode),
        actions && sectionWith("actions", "Actions", actions),
      ];
      dom.body.replaceChildren(...sections.filter(Boolean));
      dom.root.hidden = false;
      render();
      chart.set([]);
      loadHistory();
      loadEvents();
      timers.push(setInterval(() => !document.hidden && loadHistory(), HISTORY_EVERY_MS));
      timers.push(setInterval(() => !document.hidden && loadEvents(), EVENTS_EVERY_MS));
      dom.closeButton.focus({ preventScroll: true });
    },
    /** Refresh from a new snapshot; closes when the target is gone. */
    update(t, m) {
      if (id == null) return;
      if (!t) {
        close();
        return;
      }
      target = t;
      meta = m;
      render();
    },
    close,
  };
}
