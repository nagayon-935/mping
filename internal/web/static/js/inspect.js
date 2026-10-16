// Selected-host summary. Global logs, HTTP checks and routes have their own views.
import { el, statusChip } from "./dom.js";
import { dscpName, formatPct, formatRTT, lossRate, rowLevel, statusReasons } from "./model.js";
import { formatTime, formatDateTime } from "./time.js";
import { portsFor } from "./checks.js";

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

/** The parts of the Summary tab that change with each snapshot. */
function summaryParts(t, meta) {
  const asn = [t.asn, t.org, t.country && `(${t.country})`].filter(Boolean).join(" ");
  const done = t.recv + t.loss;
  const level = rowLevel(t, meta.thresholds);
  return {
    status: [statusChip(level), el("span", { text: statusReasons(t, meta.thresholds).join(" · ") })],
    columns: [
      section("Current", facts([
        ["Measurement", t.measurement_state],
        ["Health since", t.health?.since && formatTime(t.health.since)],
        ["Last probe", t.health?.last_probe_at && formatTime(t.health.last_probe_at)],
        ["Last reply", t.health?.last_reply_at && formatTime(t.health.last_reply_at)],
        ["Recent loss", t.health?.configured ? `${formatPct(t.health.recent_loss_pct)}% (${t.health.window_seconds}s window)` : null],
        ["Consecutive failures / replies", t.health?.configured ? `${t.health.consecutive_failures} / ${t.health.consecutive_successes}` : null],
        ["IP", t.ip || "–"],
        ["PTR", meta.features.ptr ? t.ptr : null],
        ["AS", meta.features.asn ? asn : null],
        ["Loss", done === 0 ? "–" : `${formatPct(lossRate(t))}% (${t.loss} of ${done})`],
        ["Sent / Recv", `${t.sent} / ${t.recv}`],
        ["Latest RTT", formatRTT(t.last_rtt_ms, false)],
        ["Jitter", formatRTT(t.jitter_ms, false)],
        ["Duplicates", t.duplicates || null],
        ["Late replies", t.late_replies || null],
        ["Path MTU", meta.features.mtu && t.pmtu ? `${t.pmtu}${t.pmtu_bottleneck_ip ? ` (bottleneck ${t.pmtu_bottleneck_ip})` : ""}` : null],
        ["Last error", t.last_error],
        ["Last loss", t.last_loss_time && formatTime(t.last_loss_time)],
      ])),
      section("Since start / reset", facts([
        ["Average RTT", formatRTT(t.avg_rtt_ms, false)],
        ["Minimum RTT", formatRTT(t.min_rtt_ms, false)],
        ["Peak RTT", formatRTT(t.max_rtt_ms, false)],
        ["Latest TTL", t.last_ttl > 0 ? t.last_ttl : "–"],
        ["DSCP", meta.features.dscp ? dscpName(t.last_dscp) : null],
        ["Started", formatDateTime(t.started_at)],
      ]), el("p", { className: "chart-caption", text: "Historical averages and peaks do not determine the current status." })),
    ],
  };
}

export function createInspect(dom) {
  let key = null;
  let status, columns, ports;
  return {
    render(view) {
      const target = view.snapshot.targets.find(t => t.id === view.selectedId);
      if (!target) return;
      dom.target.textContent = target.host;
      dom.target.title = target.host;
      const nextKey = `${target.id}:${dom.canControl()}`;
      if (key !== nextKey) {
        key = nextKey;
        status = el("div", { className: "inspect-status" });
        columns = el("div", { className: "summary-columns" });
        ports = el("div");
        const actions = dom.actionsFor(target);
        dom.body.replaceChildren(status, columns, ports,
          ...[actions && el("section", { className: "inspect-section", attrs: { "data-section": "actions" } },
            el("h3", { text: "Actions" }), actions)].filter(Boolean));
        dom.body.scrollTop = 0;
      }
      const top = dom.body.scrollTop;
      const parts = summaryParts(target, view.meta);
      status.replaceChildren(...parts.status);
      columns.replaceChildren(...parts.columns);
      const old = ports.querySelector(".table-wrap");
      const left = old?.scrollLeft ?? 0;
      ports.replaceChildren(...(view.meta.features.port ? [section("Ports", ...portsFor(target))] : []));
      const wrap = ports.querySelector(".table-wrap");
      if (wrap) wrap.scrollLeft = left;
      dom.body.scrollTop = top;
    },
    clear() {
      key = null;
      dom.body.replaceChildren();
      dom.target.textContent = "";
    },
  };
}
