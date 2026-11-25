// Dedicated MTR pane. Its table and scroll containers survive snapshots so
// live measurements never reset the user's position in a long route.
import { el, statusChip } from "./dom.js";
import { columnWidthPx, countCell, numCell, rttCell, setColumns, textCell } from "./columns.js";
import { sameValues, syncChildren, updateCells } from "./live_rows.js";
import { formatPct, selectColumns, targetAddress } from "./model.js";

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

function hopCellValues(key, hop, features) {
  switch (key) {
    case "ttl": return [hop.ttl];
    case "ip": return [hop.ip, features.ptr && hop.ptr, !hop.ip && (hop.effective_loss_level === "warn" || hop.effective_loss_level === "crit")];
    case "asn": return [hop.asn, hop.org];
    case "hopLoss": return [hop.loss_pct, hop.effective_loss_level, hop.loss_pct > hop.effective_loss_pct];
    case "sent": return [hop.sent];
    case "recv": return [hop.recv ?? 0];
    case "last": return [hop.last_rtt_ms];
    case "avg": return [hop.avg_rtt_ms];
    case "min": return [hop.min_rtt_ms];
    case "max": return [hop.max_rtt_ms];
    case "jitter": return [hop.jitter_ms];
    default: return [];
  }
}

function hopCell(key, value, detail, flagged) {
  switch (key) {
    case "ttl": return numCell(String(value));
    case "ip": return value ? textCell([value, detail].filter(Boolean).join(" "), "mono")
      : textCell("No reply", flagged ? "mono" : "mono muted");
    case "asn": return textCell([value, detail].filter(Boolean).join(" ") || "–");
    case "hopLoss": {
      const text = formatPct(value);
      if (detail === "warn" || detail === "crit") return el("td", { className: "num" }, statusChip(detail, text));
      const cell = numCell(text);
      if (flagged) cell.classList.add("muted");
      return cell;
    }
    case "sent":
    case "recv": return countCell(value);
    default: return rttCell(value);
  }
}

function targetHeading(host, address, label, count, colspan) {
  return el("tr", { className: "mtr-target-row" },
    el("th", { attrs: { colspan: String(colspan), scope: "rowgroup", title: label } },
      el("div", { className: "mtr-target-label" },
        el("span", { className: "mtr-target-kind", text: "Target" }),
        el("strong", { className: "cell-text", text: host }),
        address && el("span", { className: "mtr-target-address cell-text", text: address }),
        el("div", { className: "mtr-target-summary" },
          el("span", { className: "mtr-target-count", text: count })))));
}

function targetMessage(error, colspan) {
  return el("tr", {}, el("td", {
    className: error ? "mtr-error" : "mtr-waiting",
    text: error ? `Route discovery failed: ${error}` : "Waiting for hop replies…",
    attrs: { colspan: String(colspan) } }));
}

const renderedTables = new WeakMap();

export function renderMTR(table, targets, meta, selectedId = null) {
  const wrap = table.parentElement;
  const left = wrap.scrollLeft;
  const top = wrap.scrollTop;
  const columns = selectColumns(mtrColumns(meta.features), (c) => columnWidthPx(table, c.key), wrap.clientWidth);
  const keys = columns.map(({ key }) => key);
  const previous = renderedTables.get(table);
  const sameColumns = sameValues(previous?.keys, keys);
  if (!sameColumns) setColumns(table, columns, "ip");

  const groups = new Map();
  for (const target of targets) {
    const old = previous?.groups.get(target.id);
    const body = old?.body ?? el("tbody", { attrs: { "data-target-id": String(target.id) } });
    const hops = target.mtr_hops ?? [];
    const address = targetAddress(target);
    const label = address && target.ip ? `${target.host} · ${target.ip}` : target.host;
    const count = hops.length || target.mtr_error ? `${hops.length} ${hops.length === 1 ? "hop" : "hops"}` : "Waiting for hops";
    const headingValues = [target.host, address, label, count, columns.length];
    const heading = sameValues(old?.headingValues, headingValues) ? old.heading : targetHeading(...headingValues);
    heading.classList.toggle("is-selected", target.id === selectedId);

    const error = target.mtr_error || "";
    const message = error || !hops.length
      ? (old?.message && sameColumns && old.error === error ? old.message : targetMessage(error, columns.length))
      : null;
    const rows = message ? [heading, message] : [heading];
    const hopRows = new Map();
    for (const hop of hops) {
      const previousHop = old?.hopRows.get(hop.ttl);
      const row = previousHop?.row ?? el("tr");
      const values = updateCells(row, columns, (key) => hopCellValues(key, hop, meta.features),
        hopCell, sameColumns ? previousHop?.values : undefined);
      while (row.cells.length > columns.length) row.deleteCell(-1);
      hopRows.set(hop.ttl, { row, values });
      rows.push(row);
    }
    syncChildren(body, rows);
    groups.set(target.id, { body, heading, headingValues, message, error, hopRows });
  }

  syncChildren(table, [...groups.values()].map(({ body }) => body), table.tHead.nextElementSibling);
  renderedTables.set(table, { groups, keys });
  if (wrap.scrollLeft !== left) wrap.scrollLeft = left;
  if (wrap.scrollTop !== top) wrap.scrollTop = top;
  return targets.length;
}
