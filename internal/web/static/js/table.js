// Ping monitor table. Rebuilt from the snapshot on every update; row focus
// and selection are restored by target ID.
import { el, statusChip } from "./dom.js";
import { countCell, numCell, rttCell, textCell } from "./cells.js";
import { columnWidthPx, setColumns } from "./columns.js";
import { SORTS } from "./workspace.js";
import {
  buildSections, cellLevels, dscpName, formatPct, isIPLiteral, lossRate, matchesFilter, rowLevel,
  selectColumns, statusReasons,
} from "./model.js";

/** The overview keeps current measurements; historical RTT statistics live
 * in Summary and the separate graph pane carries all time-series detail. */
export function targetColumns(features, targets = []) {
  // Use configured identities, not changing DNS results or filtered rows,
  // so receiving measurements and searching cannot add/remove this column.
  const showIP = !targets.length || targets.some((t) => !isIPLiteral(t.host));
  return [
    { key: "status", label: "Status", required: true, title: "Health based on recent measurements; the reason appears below each status" },
    { key: "host", label: "Host", required: true },
    showIP && { key: "ip", label: "IP", priority: 6 },
    features.asn && { key: "asn", label: "AS", priority: 8 },
    { key: "loss", label: "Loss %", required: true, title: "Lifetime loss over completed probes; status uses recent loss and consecutive failures", num: true },
    { key: "sent", label: "Sent", num: true, priority: 4 },
    { key: "recv", label: "Recv", num: true, priority: 5 },
    { key: "last", label: "RTT", num: true, required: true, title: "Round-trip time of the latest probe" },
    { key: "jitter", label: "Jitter", num: true, priority: 3 },
    { key: "ttl", label: "TTL", num: true, priority: 7 },
    features.dscp && { key: "dscp", label: "DSCP", priority: 9 },
  ].filter(Boolean);
}

function hostCell(t, features, group, focused) {
  const td = el("td", { className: "host-cell" },
    el("button", { className: "host-open", attrs: { type: "button", tabindex: focused ? "0" : "-1", "data-target-id": String(t.id), title: t.host, "aria-label": `Inspect ${t.host}` } },
      el("span", { className: "host", text: t.host }),
      el("span", { className: "host-arrow", text: "›", attrs: { "aria-hidden": "true" } })));
  if (group) td.append(el("span", { className: "sub", text: group, attrs: { title: `Group: ${group}` } }));
  else if (features.ptr && t.ptr && t.ptr !== t.host) td.append(el("span", { className: "sub", text: t.ptr, attrs: { title: t.ptr } }));
  return td;
}

function targetCell(col, t, th, cells, features, group, focused) {
  switch (col.key) {
    case "status": {
      // An OK row needs no explanation; leaving it blank makes the rows that
      // do need attention stand out. Summary still states the reason.
      const level = rowLevel(t, th);
      const reasons = statusReasons(t, th).join(" · ");
      return el("td", { attrs: { title: reasons } }, statusChip(level),
        el("span", { className: "status-reason", text: level === "ok" ? "" : reasons }));
    }
    case "host": return hostCell(t, features, group, focused);
    case "ip": return textCell(t.ip || "–", "mono");
    case "asn": return textCell([t.asn, t.org].filter(Boolean).join(" ") || "–");
    case "loss": return metricCell(numCell(t.recv + t.loss === 0 ? "–" : formatPct(lossRate(t)), cells.loss));
    case "sent": return countCell(t.sent);
    case "recv": return countCell(t.recv);
    case "last": return metricCell(rttCell(t.last_rtt_ms, cells.last));
    case "jitter": return metricCell(rttCell(t.jitter_ms, cells.jitter));
    case "ttl": return numCell(t.last_ttl > 0 ? String(t.last_ttl) : "–");
    case "dscp": return el("td", { text: dscpName(t.last_dscp) });
    default: return el("td");
  }
}

function metricCell(td) {
  const text = td.textContent;
  td.replaceChildren(el("span", { className: "metric-value", text }));
  return td;
}

/**
 * @param {HTMLTableElement} table
 * @param {object} view {snapshot, meta, filter, selectedId}
 * @param {(id: number) => void} onOpen
 * @returns {number} rows rendered (after filtering)
 */
export function renderTargets(table, view, onOpen, onSort) {
  const { snapshot, meta, filter, selectedId } = view;
  const th = meta.thresholds;
  // The table is as wide as its wrapper; widths come from the CSS contract,
  // which already reflects the current breakpoint and pane layout.
  const available = table.parentElement.clientWidth;
  const columns = selectColumns(targetColumns(meta.features, snapshot.targets), (c) => columnWidthPx(table, c.key), available);
  const active = document.activeElement;
  const focusedSortBefore = table.contains(active) ? active?.dataset?.sort : null;
  setColumns(table, columns, "host");
  const focusedId = table.contains(active) ? active?.dataset?.targetId : null;
  const focusedSort = focusedSortBefore;
  const visible = view.visibleTargets ?? snapshot.targets.filter((t) => matchesFilter(t, filter, meta.features));
  const focusId = visible.some(t => String(t.id) === focusedId) ? Number(focusedId)
    : visible.some(t => t.id === view.focusedId) ? view.focusedId
    : visible.some(t => t.id === selectedId) ? selectedId : visible[0]?.id;
  for (const th of table.tHead.querySelectorAll("th")) {
    const key = columns[[...th.parentElement.children].indexOf(th)].key;
    if (!onSort || !SORTS[key]) continue;
    const current = view.sort?.key === key;
    th.setAttribute("aria-sort", current ? view.sort.direction === "asc" ? "ascending" : "descending" : "none");
    const button = el("button", { className: "sort-heading", text: `${th.textContent}${current ? view.sort.direction === "asc" ? " ↑" : " ↓" : ""}`,
      attrs: { type: "button", "data-sort": key, "aria-label": `Sort by ${SORTS[key]}` } });
    button.addEventListener("click", () => onSort(key));
    th.replaceChildren(button);
  }
  const groupsById = new Map(meta.groups?.flatMap(g => g.target_ids.map(id => [id, g.name])) ?? []);
  const rows = [];
  for (const section of buildSections(visible, view.sort?.key && view.sort.key !== "config" ? [] : meta.groups)) {
    if (section.name != null) {
      rows.push(el("tr", { className: "group-row" },
        el("td", { text: `${section.name} (${section.targets.length})`, attrs: { colspan: String(columns.length) } })));
    }
    for (const t of section.targets) {
      const cells = cellLevels(t, th);
      const tr = el("tr", {
        className: `target-row health-${rowLevel(t, th)}`,
        attrs: {
          tabindex: "-1",
          ...(t.id === selectedId ? { "aria-current": "true" } : {}),
          "aria-label": `${t.host}, select to inspect`,
          "data-target-id": String(t.id),
        },
      });
      for (const c of columns) {
        const cell = targetCell(c, t, th, cells, meta.features, view.sort?.key && view.sort.key !== "config" ? groupsById.get(t.id) : null, t.id === focusId);
        cell.classList.add(`col-${c.key}`);
        tr.append(cell);
      }
      tr.addEventListener("click", () => onOpen(t.id));
      tr.querySelector(".host-open").addEventListener("keydown", e => {
        if (e.isComposing || e.ctrlKey || e.metaKey || e.altKey) return;
        if (e.key === "Enter" || e.key === " ") { e.preventDefault(); e.stopPropagation(); onOpen(t.id); }
      });
      tr.addEventListener("keydown", (e) => {
        if (e.isComposing || e.ctrlKey || e.metaKey || e.altKey) return;
        if (e.target === tr && (e.key === "Enter" || e.key === " ")) {
          e.preventDefault();
          onOpen(t.id);
        }
      });
      rows.push(tr);
    }
  }
  table.tBodies[0].replaceChildren(...rows);

  if (focusedId && table.contains(active) === false) table.querySelector(`.host-open[data-target-id="${CSS.escape(focusedId)}"]`)?.focus({ preventScroll: true });
  if (focusedSort) table.querySelector(`[data-sort="${CSS.escape(focusedSort)}"]`)?.focus({ preventScroll: true });
  return visible.length;
}
