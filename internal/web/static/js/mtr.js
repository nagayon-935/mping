// Dedicated MTR pane. Its table and scroll containers survive snapshots so
// live measurements never reset the user's position in a long route.
import { el, statusChip } from "./dom.js";
import { countCell, numCell, rttCell, textCell } from "./cells.js";
import { columnWidthPx, setColumns } from "./columns.js";
import { formatPct, hopLossLevel, selectColumns, targetAddress } from "./model.js";

/** Hop, address, loss and latest/average RTT always show; the rest appear as
 * the pane widens, in priority order, like the target table. */
export function mtrColumns(features) {
  return [
    { key: "ttl", label: "Hop", num: true, required: true },
    { key: "ip", label: "Address", required: true },
    features.asn && { key: "asn", label: "AS", priority: 3 },
    { key: "hopLoss", label: "Loss %", num: true, required: true },
    { key: "sent", label: "Sent", num: true, priority: 5 },
    { key: "recv", label: "Recv", num: true, priority: 6 },
    { key: "last", label: "RTT", num: true, required: true },
    { key: "avg", label: "Avg", num: true, required: true },
    { key: "min", label: "Best", num: true, priority: 2 },
    { key: "max", label: "Worst", num: true, priority: 1 },
    { key: "jitter", label: "Jitter", num: true, priority: 4 },
  ].filter(Boolean);
}

function hopCell(key, hop, thresholds) {
  switch (key) {
    case "ttl": return numCell(String(hop.ttl));
    case "ip": return textCell(hop.ip || "No reply", "mono");
    case "asn": return textCell([hop.asn, hop.org].filter(Boolean).join(" ") || "–");
    case "hopLoss": return el("td", { className: "num" }, statusChip(hopLossLevel(hop.loss_pct, thresholds), formatPct(hop.loss_pct)));
    case "sent": return countCell(hop.sent);
    case "recv": return countCell(hop.recv ?? 0);
    case "last": return rttCell(hop.last_rtt_ms);
    case "avg": return rttCell(hop.avg_rtt_ms);
    case "min": return rttCell(hop.min_rtt_ms);
    case "max": return rttCell(hop.max_rtt_ms);
    case "jitter": return rttCell(hop.jitter_ms);
    default: return el("td");
  }
}

export function renderMTR(table, targets, meta, selectedId = null) {
  const wrap = table.parentElement;
  const left = wrap.scrollLeft;
  const top = wrap.scrollTop;
  const columns = selectColumns(mtrColumns(meta.features), (c) => columnWidthPx(table, c.key), wrap.clientWidth);
  setColumns(table, columns, "ip");
  const groups = targets.map((target) => {
    const body = el("tbody", { attrs: { "data-target-id": String(target.id) } });
    const hops = target.mtr_hops ?? [];
    const address = targetAddress(target);
    const label = address && target.ip ? `${target.host} · ${target.ip}` : target.host;
    body.append(el("tr", { className: `mtr-target-row${target.id === selectedId ? " is-selected" : ""}` },
      el("th", { attrs: { colspan: String(columns.length), scope: "rowgroup", title: label } },
        el("div", { className: "mtr-target-label" },
          el("span", { className: "mtr-target-kind", text: "Target" }),
          el("strong", { className: "cell-text", text: target.host }),
          el("span", { className: "mtr-target-count", text: hops.length ? `${hops.length} hops` : "Waiting" }),
          address && el("span", { className: "mtr-target-address cell-text", text: address })))));
    if (!hops.length) body.append(el("tr", {}, el("td", { className: "mtr-waiting", text: "Waiting for hop replies. No MTR route data has been received yet.", attrs: { colspan: String(columns.length) } })));
    for (const hop of hops) {
      body.append(el("tr", {}, ...columns.map((c) => {
        const cell = hopCell(c.key, hop, meta.thresholds);
        cell.classList.add(`col-${c.key}`);
        return cell;
      })));
    }
    return body;
  });
  for (const body of [...table.tBodies]) body.remove();
  table.append(...groups);
  wrap.scrollLeft = left;
  wrap.scrollTop = top;
  return targets.length;
}
