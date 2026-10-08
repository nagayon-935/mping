// Ping monitor table. Rebuilt from the snapshot on every update; row focus
// and selection are restored by target ID.
import { el, statusChip } from "./dom.js";
import { drawSparkline } from "./chart.js";
import { countCell, numCell, rttCell, textCell } from "./cells.js";
import { columnWidthPx, cssPx, setColumns } from "./columns.js";
import {
  buildSections, cellLevels, dscpName, formatPct, lossRate, matchesFilter, rowLevel,
  selectColumns, statusReasons, tableScale,
} from "./model.js";

// Spare width is turned into larger text and columns, up to this factor;
// beyond it the remainder stays empty (very large monitors).
const MAX_TABLE_SCALE = 2;

/**
 * Every column the TUI offers for the enabled features, in display order.
 * `priority` decides what survives when the table is narrow (lower first):
 * loss, then latest/average/jitter RTT, then the trend, then the rest.
 * Status and host identify the row and are always shown.
 */
function targetColumns(features) {
  return [
    { key: "status", label: "Status", required: true },
    { key: "host", label: "Host", required: true },
    { key: "ip", label: "IP", priority: 6 },
    features.asn && { key: "asn", label: "AS", priority: 12 },
    { key: "loss", label: "Loss %", num: true, priority: 1 },
    { key: "sent", label: "Sent", num: true, priority: 9 },
    { key: "recv", label: "Recv", num: true, priority: 10 },
    { key: "last", label: "Last", num: true, priority: 2 },
    { key: "avg", label: "Avg", num: true, priority: 3, title: "Average RTT since start or last reset; does not determine status" },
    { key: "min", label: "Min", num: true, priority: 7, title: "Minimum RTT since start or last reset" },
    { key: "max", label: "Peak", num: true, priority: 8, title: "Peak RTT since start or last reset; does not determine status" },
    { key: "jitter", label: "Jitter", num: true, priority: 4 },
    { key: "ttl", label: "TTL", num: true, priority: 11 },
    features.dscp && { key: "dscp", label: "DSCP", priority: 13 },
    { key: "spark", label: "RTT trend", priority: 5 },
  ].filter(Boolean);
}

function hostCell(t) {
  const td = el("td", { className: "host-cell" },
    el("button", { className: "host-open", attrs: { type: "button", tabindex: "-1", title: t.host, "aria-label": `Inspect ${t.host}` } },
      el("span", { className: "host", text: t.host }),
      el("span", { className: "host-arrow", text: "›", attrs: { "aria-hidden": "true" } })));
  if (t.ptr && t.ptr !== t.host) td.append(el("span", { className: "sub", text: t.ptr, attrs: { title: t.ptr } }));
  return td;
}

function targetCell(col, t, th, cells, sparks) {
  switch (col.key) {
    case "status": {
      const reasons = statusReasons(t, th).join(" · ");
      return el("td", {}, statusChip(rowLevel(t, th)),
        el("span", { className: "status-reason", text: reasons, attrs: { title: reasons } }));
    }
    case "host": return hostCell(t);
    case "ip": return textCell(t.ip || "–", "mono");
    case "asn": return textCell([t.asn, t.org].filter(Boolean).join(" ") || "–");
    case "loss": return numCell(t.recv + t.loss === 0 ? "–" : formatPct(lossRate(t)), cells.loss);
    case "sent": return countCell(t.sent);
    case "recv": return countCell(t.recv);
    case "last": return rttCell(t.last_rtt_ms, cells.last);
    // These describe the whole run, not the measurements that drive status.
    case "avg": return rttCell(t.avg_rtt_ms);
    case "min": return rttCell(t.min_rtt_ms);
    case "max": return rttCell(t.max_rtt_ms);
    case "jitter": return rttCell(t.jitter_ms, cells.jitter);
    case "ttl": return numCell(t.last_ttl > 0 ? String(t.last_ttl) : "–");
    case "dscp": return el("td", { text: dscpName(t.last_dscp) });
    case "spark": {
      const canvas = el("canvas", { className: "spark", attrs: { "aria-hidden": "true" } });
      sparks.push([canvas, t.id]);
      return el("td", {}, canvas);
    }
    default: return el("td");
  }
}

/**
 * @param {HTMLTableElement} table
 * @param {object} view {snapshot, meta, filter, selectedId, history: Map<id, series>}
 * @param {(id: number) => void} onOpen
 * @returns {number} rows rendered (after filtering)
 */
export function renderTargets(table, view, onOpen) {
  const { snapshot, meta, filter, selectedId, history } = view;
  const th = meta.thresholds;
  // The table is as wide as its wrapper; widths come from the CSS contract,
  // which already reflects the current breakpoint and pane layout.
  const available = table.parentElement.clientWidth;
  const columns = selectColumns(targetColumns(meta.features), (c) => columnWidthPx(table, c.key), available);
  setColumns(table, columns, "host");
  // Fill the width: zoom the whole table (text, columns, row heights) by the
  // ratio of the width we have to the widest the chosen columns can be.
  const widest = columns.reduce((sum, c) => sum + columnWidthPx(table, c.key), 0) + cssPx(table, "--host-grow");
  const scale = tableScale(widest, available, MAX_TABLE_SCALE);
  table.style.zoom = scale > 1 ? String(scale) : "";

  const focusedId = document.activeElement?.dataset?.targetId;
  const visible = snapshot.targets.filter((t) => matchesFilter(t, filter));
  const rows = [];
  const sparks = [];
  for (const section of buildSections(visible, meta.groups)) {
    if (section.name != null) {
      rows.push(el("tr", { className: "group-row" },
        el("td", { text: `${section.name} (${section.targets.length})`, attrs: { colspan: String(columns.length) } })));
    }
    for (const t of section.targets) {
      const cells = cellLevels(t, th);
      const tr = el("tr", {
        className: "target-row",
        attrs: {
          tabindex: "0",
          ...(t.id === selectedId ? { "aria-current": "true" } : {}),
          "aria-label": `${t.host}, select to inspect`,
          "data-target-id": String(t.id),
        },
      });
      for (const c of columns) {
        const cell = targetCell(c, t, th, cells, sparks);
        cell.classList.add(`col-${c.key}`);
        tr.append(cell);
      }
      tr.addEventListener("click", () => onOpen(t.id));
      tr.addEventListener("keydown", (e) => {
        if (e.target === tr && (e.key === "Enter" || e.key === " ")) {
          e.preventDefault();
          onOpen(t.id);
        }
      });
      rows.push(tr);
    }
  }
  table.tBodies[0].replaceChildren(...rows);

  for (const [canvas, id] of sparks) drawSparkline(canvas, history.get(id));
  if (focusedId) table.querySelector(`tr[data-target-id="${CSS.escape(focusedId)}"]`)?.focus({ preventScroll: true });
  return visible.length;
}
