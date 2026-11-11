// Selected-host Summary: header (host, status pill, since), the reason, four
// KPI tiles, the RTT chart, recent events and the remaining facts in a
// collapsible section. Global logs, HTTP checks and routes have their own views.
import { el, readSession, statusChip, writeSession } from "./dom.js";
import { formatMs, formatPct, formatRTT, lossRate, rowLevel, statusReason } from "./model.js";
import { formatTime, formatDateTime } from "./time.js";
import { portsFor } from "./checks.js";
import { explainBreach, parseBreaches, ttlHops } from "./health_text.js";
import { createSummaryChart } from "./summary_chart.js";
import { createRecentEvents } from "./inspect_events.js";

const DETAILS_KEY = "mping.summaryDetails";
const LEVEL_LABELS = { ok: "OK", warn: "Warning", crit: "Critical", pending: "Waiting" };

function facts(pairs) {
  const dl = el("dl", { className: "facts" });
  for (const [k, v] of pairs) {
    if (v == null || v === "") continue;
    dl.append(el("dt", { text: k }), el("dd", { text: String(v) }));
  }
  return dl;
}

function section(title, ...children) {
  return el("section", { className: "inspect-section" }, el("h3", { text: title }), ...children);
}

/** "dns.google · AS15169 Google LLC · Critical since 12:04:11" */
function subline(t, meta) {
  const level = rowLevel(t);
  const asn = meta.features.asn ? [t.asn, t.org].filter(Boolean).join(" ") : "";
  const ptr = meta.features.ptr && t.ptr && t.ptr !== t.host ? t.ptr : "";
  const ip = t.ip && t.ip !== t.host ? t.ip : "";
  const since = t.health?.since ? `${LEVEL_LABELS[level]} since ${formatTime(t.health.since)}` : "";
  return [ptr || ip, asn, since].filter(Boolean).join(" · ");
}

/** Why the host has its status, one line per breach with its threshold. */
function reasonBanner(t, meta) {
  const level = rowLevel(t);
  const breaches = parseBreaches(t.health?.reason);
  const lines = breaches.length
    ? breaches.map((b) => {
      const { lead, rest } = explainBreach(b, meta.thresholds, t.levels);
      return el("span", {}, el("strong", { text: lead }), ` ${rest}.`);
    })
    : [el("span", { text: statusReason(t) })];
  return el("div", { className: `reason-banner lvl-${level}`, attrs: { role: "note" } },
    statusChip(level), el("div", { className: "reason-lines" }, ...lines));
}

/** Breach level of one metric from the server's reason ("none" when not breached). */
function breachLevel(t, metric, crit) {
  const b = parseBreaches(t.health?.reason).find((x) => x.metric === metric);
  if (!b) return "none";
  return crit > 0 && b.value > crit ? "crit" : "warn";
}

function tile(label, value, sub, level = "none") {
  return el("div", { className: `kpi lvl-${level}` },
    el("span", { className: "kpi-label", text: label }),
    el("span", { className: "kpi-value", text: value }),
    el("span", { className: "kpi-sub", text: sub }));
}

const number = (ms) => formatMs(ms);
const thresholdText = (warn, crit, level, unit) => {
  const show = level === "crit" ? ["crit", crit] : ["warn", warn];
  return show[1] > 0 ? `${unit} · ${show[0]} > ${formatMs(show[1])}` : unit;
};

function kpis(t, meta) {
  const th = meta.thresholds ?? {};
  const rttLevel = t.levels?.last_rtt ?? "none";
  const jitterLevel = t.levels?.jitter ?? "none";
  const lossLevel = breachLevel(t, "Loss", th.loss_crit_pct);
  const done = t.recv + t.loss;
  const recent = t.health?.configured && t.health.recent_completed > 0;
  const hops = ttlHops(t.last_ttl);
  return el("div", { className: "kpis" },
    tile("RTT", number(t.last_rtt_ms), t.avg_rtt_ms > 0 ? `ms · avg ${formatMs(t.avg_rtt_ms)}` : "ms", rttLevel),
    tile("Jitter", number(t.jitter_ms), thresholdText(th.jitter_warn_ms, th.jitter_crit_ms, jitterLevel, "ms"), jitterLevel),
    tile(t.health?.configured ? `Loss ${Math.round(t.health.window_seconds)}s` : "Loss",
      recent ? formatPct(t.health.recent_loss_pct) : "–",
      `% · lifetime ${done ? formatPct(lossRate(t)) : "–"}`, lossLevel),
    tile("TTL", t.last_ttl > 0 ? String(t.last_ttl) : "–", hops == null ? "latest reply" : `≈ ${hops} hops`));
}

/** Everything else, collapsed under one disclosure. */
function detailColumns(t, meta) {
  const asn = [t.asn, t.org, t.country && `(${t.country})`].filter(Boolean).join(" ");
  const done = t.recv + t.loss;
  return [
    section("Current", facts([
      ["IP", t.ip || "–"],
      ["Measurement", t.measurement_state],
      ["Health since", t.health?.since && formatTime(t.health.since)],
      ["Last reply", t.health?.last_reply_at && formatTime(t.health.last_reply_at)],
      ["Last probe", t.health?.last_probe_at && formatTime(t.health.last_probe_at)],
      ["Recent loss", t.health?.configured ? `${formatPct(t.health.recent_loss_pct)}% (${t.health.recent_loss} of ${t.health.recent_completed}, ${t.health.window_seconds}s window)` : "Not available"],
      ["Consecutive failures / replies", t.health?.configured ? `${t.health.consecutive_failures} / ${t.health.consecutive_successes}` : null],
      ["Full reason", statusReason(t)],
    ])),
    section("Since start / reset", facts([
      ["Lifetime loss", done === 0 ? "–" : `${formatPct(lossRate(t))}% (${t.loss} of ${done})`],
      ["Sent / Recv", `${t.sent} / ${t.recv}`],
      ["Min / Avg / Peak", `${formatRTT(t.min_rtt_ms, false)} / ${formatRTT(t.avg_rtt_ms, false)} / ${formatRTT(t.max_rtt_ms, false)}`],
      ["Duplicates", t.duplicates || null],
      ["Late replies", t.late_replies || null],
    ]), el("p", { className: "chart-caption", text: "Lifetime statistics do not determine current health." })),
    section("Network details", facts([
      ["PTR", meta.features.ptr ? t.ptr : null],
      ["AS", meta.features.asn ? asn : null],
      ["Path MTU", meta.features.mtu ? (t.path_mtu ? `${t.path_mtu} bytes (${t.pmtu_status})` : "Unknown") : null],
      ["PMTU payload", meta.features.mtu && t.pmtu ? `${t.pmtu} bytes${t.pmtu_bottleneck_ip ? ` (bottleneck ${t.pmtu_bottleneck_ip})` : ""}` : null],
      ["Started", formatDateTime(t.started_at)],
      ["Last error", t.last_error],
      ["Last loss", t.last_loss_time && formatTime(t.last_loss_time)],
    ])),
  ];
}

/**
 * @param {{host: HTMLElement, pill: HTMLElement, target: HTMLElement, body: HTMLElement,
 *          canControl: () => boolean, isStopped: () => boolean, actionsFor: (t: object) => Node|null}} dom
 */
export function createInspect(dom) {
  const chart = createSummaryChart();
  const events = createRecentEvents({ isStopped: dom.isStopped });
  let key = null;
  let banner, tiles, details, columns, ports;
  return {
    windowSelect: chart.windowSelect,
    render(view) {
      const target = view.snapshot.targets.find((t) => t.id === view.selectedId);
      if (!target) return;
      dom.host.textContent = target.host;
      dom.host.title = target.host;
      dom.pill.replaceChildren(el("span", { className: `pill lvl-${rowLevel(target)}` }, statusChip(rowLevel(target))));
      dom.target.textContent = subline(target, view.meta);
      dom.target.title = dom.target.textContent;
      const nextKey = `${target.id}:${dom.canControl()}`;
      if (key !== nextKey) {
        key = nextKey;
        banner = el("div");
        tiles = el("div");
        columns = el("div", { className: "summary-columns" });
        ports = el("div");
        details = el("details", { className: "inspect-details" },
          el("summary", { text: "Details (current, since start, network)" }), columns, ports);
        details.open = readSession(DETAILS_KEY) !== "false";
        details.addEventListener("toggle", () => writeSession(DETAILS_KEY, String(details.open)));
        const actions = dom.actionsFor(target);
        dom.body.replaceChildren(banner, tiles, chart.node, events.node, details,
          ...[actions && el("section", { className: "inspect-section", attrs: { "data-section": "actions" } },
            el("h3", { text: "Actions" }), actions)].filter(Boolean));
        dom.body.scrollTop = 0;
      }
      events.show(target.id);
      const top = dom.body.scrollTop;
      banner.replaceChildren(reasonBanner(target, view.meta));
      tiles.replaceChildren(kpis(target, view.meta));
      chart.render({
        target, samples: view.series.get(target.id), meta: view.meta,
        points: view.window?.points ?? 0, now: view.historyNow ?? Date.now(), windowLabel: view.window?.label,
      });
      columns.replaceChildren(...detailColumns(target, view.meta));
      const old = ports.querySelector(".table-wrap");
      const left = old?.scrollLeft ?? 0;
      ports.replaceChildren(...(view.meta.features.port ? [section("Ports", portsFor(target))] : []));
      const wrap = ports.querySelector(".table-wrap");
      if (wrap) wrap.scrollLeft = left;
      dom.body.scrollTop = top;
    },
    clear() {
      key = null;
      chart.reset();
      events.show(null);
      dom.body.replaceChildren();
      dom.host.textContent = "";
      dom.pill.replaceChildren();
      dom.target.textContent = "";
    },
  };
}
