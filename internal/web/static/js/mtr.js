// Dedicated MTR pane. Its table and scroll containers survive snapshots so
// live measurements never reset the user's position in a long route.
import { el, statusChip } from "./dom.js";
import { countCell, numCell, rttCell, textCell } from "./cells.js";
import { setColumns } from "./columns.js";
import { formatPct, hopLossLevel } from "./model.js";

export function renderMTR(table, targets, meta) {
  const columns = [
    { key: "ttl", label: "Hop", num: true },
    { key: "ip", label: "Address" },
    ...(meta.features.asn ? [{ key: "asn", label: "AS" }] : []),
    { key: "hopLoss", label: "Loss %", num: true },
    { key: "sent", label: "Sent", num: true },
    { key: "recv", label: "Recv", num: true },
    { key: "last", label: "RTT", num: true },
    { key: "avg", label: "Avg", num: true },
    { key: "min", label: "Best", num: true },
    { key: "max", label: "Worst", num: true },
    { key: "jitter", label: "Jitter", num: true },
  ];
  const wrap = table.parentElement;
  const left = wrap.scrollLeft;
  const top = wrap.scrollTop;
  setColumns(table, columns, "ip");
  const groups = targets.map((target) => {
    const body = el("tbody", { attrs: { "data-target-id": String(target.id) } });
    const hops = target.mtr_hops ?? [];
    const label = target.ip && target.ip !== target.host ? `${target.host} · ${target.ip}` : target.host;
    body.append(el("tr", { className: "mtr-target-row" },
      el("th", { attrs: { colspan: String(columns.length), scope: "rowgroup", title: label } },
        el("div", { className: "mtr-target-label" },
          el("span", { className: "mtr-target-kind", text: "Target" }),
          el("strong", { className: "cell-text", text: target.host }),
          el("span", { className: "mtr-target-count", text: hops.length ? `${hops.length} hops` : "Waiting" }),
          el("span", { className: "mtr-target-address cell-text", text: target.ip || "Resolving address…" })))));
    if (!hops.length) body.append(el("tr", {}, el("td", { className: "mtr-waiting", text: "No MTR data for this target yet.", attrs: { colspan: String(columns.length) } })));
    for (const hop of hops) {
      const cells = [
        numCell(String(hop.ttl)), textCell(hop.ip || "No reply", "mono"),
        ...(meta.features.asn ? [textCell([hop.asn, hop.org].filter(Boolean).join(" ") || "–")] : []),
        el("td", { className: "num" }, statusChip(hopLossLevel(hop.loss_pct, meta.thresholds), formatPct(hop.loss_pct))),
        countCell(hop.sent), countCell(hop.recv ?? 0), rttCell(hop.last_rtt_ms), rttCell(hop.avg_rtt_ms),
        rttCell(hop.min_rtt_ms), rttCell(hop.max_rtt_ms), rttCell(hop.jitter_ms),
      ];
      cells.forEach((cell, i) => cell.classList.add(`col-${columns[i].key}`));
      body.append(el("tr", {}, ...cells));
    }
    return body;
  });
  for (const body of [...table.tBodies]) body.remove();
  table.append(...groups);
  wrap.scrollLeft = left;
  wrap.scrollTop = top;
  return targets.length;
}
