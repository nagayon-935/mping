// Ping monitor table. Keep live rows keyed by target ID.
import { badgeIcon, el, statusChip, statusLabel } from "./dom.js";
import { columnWidthPx, countCell, numCell, rttCell, setColumns, textCell } from "./columns.js";
import { sameValues, syncChildren, updateCells } from "./live_rows.js";
import { SORTS } from "./workspace.js";
import { shortReason } from "./health_text.js";
import { sparkline } from "./spark_view.js";
import {
  buildSections, cellLevels, formatPct, formatRTT, groupSummary, isIPLiteral, lossRate, matchesFilter, rowLevel,
  selectColumns, statusReason, targetStatus,
} from "./model.js";

/** The overview keeps current measurements plus a one-minute RTT sparkline;
 * historical statistics and the large chart live in Summary. */
export function targetColumns(features, targets = [], sparkMax) {
  // Use configured identities, not changing DNS results or filtered rows,
  // so receiving measurements and searching cannot add/remove this column.
  const showIP = !targets.length || targets.some((t) => !isIPLiteral(t.host));
  return [
    { key: "status", label: "Status", required: true, title: "Health based on recent measurements; the reason appears below each status" },
    { key: "host", label: "Host", required: true },
    showIP && { key: "ip", label: "IP", priority: 6 },
    features.asn && { key: "asn", label: "AS", priority: 8 },
    { key: "spark", label: sparkMax == null ? "RTT · 60s" : `RTT · 60s (0–${formatRTT(sparkMax)})`, priority: 2, title: "RTT over the last 60 seconds on a scale shared by all rows; red ticks are lost probes, a dashed red line means no replies" },
    { key: "loss", label: "Loss %", required: true, title: "Lifetime loss over completed probes; status uses recent loss and consecutive failures", num: true },
    { key: "sent", label: "Sent", num: true, priority: 4 },
    { key: "recv", label: "Recv", num: true, priority: 5 },
    { key: "last", label: "RTT", num: true, required: true, title: "Round-trip time of the latest probe" },
    { key: "jitter", label: "Jitter", num: true, priority: 3 },
    { key: "ttl", label: "TTL", num: true, priority: 7 },
  ].filter(Boolean);
}

function hostCell(host, id, group, ptr) {
  const td = el("td", { className: "host-cell" },
    el("button", { className: "host-open", attrs: { type: "button", tabindex: "-1", "data-target-id": String(id), title: host, "aria-label": `Inspect ${host}` } },
      el("span", { className: "host", text: host }),
      el("span", { className: "host-arrow", text: "›", attrs: { "aria-hidden": "true" } })));
  if (group) td.append(el("span", { className: "sub", text: group, attrs: { title: `Group: ${group}` } }));
  else if (ptr) td.append(el("span", { className: "sub", text: ptr, attrs: { title: ptr } }));
  return td;
}

function targetCellValues(key, t, levels, features, group, sparks) {
  switch (key) {
    case "status": {
      const level = targetStatus(t);
      return [level, level === "stopped" ? "" : statusReason(t)];
    }
    case "spark": return [sparks?.series.get(t.id), sparks?.max];
    case "host": return [t.host, t.id, group, features.ptr && t.ptr !== t.host ? t.ptr : null];
    case "ip": return [t.ip];
    case "asn": return [t.asn, t.org];
    case "loss": return [t.recv + t.loss ? lossRate(t) : null, levels.loss];
    case "sent": return [t.sent];
    case "recv": return [t.recv];
    case "last": return [t.last_rtt_ms, levels.last];
    case "jitter": return [t.jitter_ms, levels.jitter];
    case "ttl": return [t.last_ttl];
    default: return [];
  }
}

function targetCell(key, value, detail, ...hostDetails) {
  switch (key) {
    case "status": return el("td", { attrs: { title: detail } }, statusChip(value),
      el("span", { className: "status-reason", text: value === "ok" ? "" : shortReason(detail) }));
    case "spark": return el("td", { className: "spark-cell" }, value ? sparkline(value, detail) : null);
    case "host": return hostCell(value, detail, ...hostDetails);
    case "ip": return textCell(value || "–", "mono");
    case "asn": return textCell([value, detail].filter(Boolean).join(" ") || "–");
    case "loss": return metricCell(numCell(value == null ? "–" : formatPct(value)), detail);
    case "sent":
    case "recv": return countCell(value);
    case "last":
    case "jitter": return metricCell(rttCell(value), detail);
    case "ttl": return numCell(value > 0 ? String(value) : "–");
    default: return el("td");
  }
}

function metricCell(td, level) {
  const value = el("span", { className: "metric-value" });
  if (level === "warn" || level === "crit") {
    td.classList.add(`lvl-${level}`);
    value.append(el("span", { className: "metric-icon", text: badgeIcon(level), attrs: { "aria-hidden": "true" } }));
  }
  value.append(td.textContent);
  td.replaceChildren(value);
  return td;
}

const renderedTables = new WeakMap();

function renderHead(table, columns, sort, onSort) {
  setColumns(table, columns, "host");
  [...table.tHead.rows[0].cells].forEach((th, i) => {
    const key = columns[i].key;
    if (!onSort || !SORTS[key]) return;
    const current = sort?.key === key;
    th.setAttribute("aria-sort", current ? sort.direction === "asc" ? "ascending" : "descending" : "none");
    const button = el("button", { className: "sort-heading", text: `${th.textContent}${current ? sort.direction === "asc" ? " ↑" : " ↓" : ""}`,
      attrs: { type: "button", "data-sort": key, "aria-label": `Sort by ${SORTS[key]}` } });
    button.addEventListener("click", () => onSort(key));
    th.replaceChildren(button);
  });
}

function groupRow(name, count, summary, colspan) {
  const heading = el("td", { text: `${name} (${count})`, attrs: { colspan: String(colspan) } });
  for (const { level, label } of summary) {
    const chip = statusChip(level, label);
    chip.setAttribute("role", "img");
    chip.setAttribute("aria-label", level === "ok" ? label : `${statusLabel(level)}: ${label}`);
    heading.append(" · ", chip);
  }
  return el("tr", { className: "group-row" }, heading);
}

function makeTargetRow(id, onOpen) {
  const node = el("tr", { attrs: { tabindex: "-1", "data-target-id": String(id) } });
  node.addEventListener("click", () => onOpen(id));
  node.addEventListener("keydown", (e) => {
    if (e.isComposing || e.ctrlKey || e.metaKey || e.altKey) return;
    if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      e.stopPropagation();
      onOpen(id);
    }
  });
  return { node };
}

export function renderTargets(table, view, onOpen, onSort) {
  const { snapshot, meta, filter, selectedId, sort, sparks } = view;
  const available = table.parentElement.clientWidth;
  const columns = selectColumns(targetColumns(meta.features, snapshot.targets, sparks?.max), (c) => columnWidthPx(table, c.key), available);
  const keys = columns.map(({ key }) => key);
  const head = [sort?.key, sort?.direction, onSort, ...columns.map(({ label }) => label)];
  const previous = renderedTables.get(table);
  const sameColumns = sameValues(previous?.keys, keys);
  const active = document.activeElement;
  const focusedSort = table.contains(active) ? active?.dataset?.sort : null;
  const focusedId = table.contains(active) ? active?.dataset?.targetId : null;
  if (!sameColumns || !sameValues(previous?.head, head)) renderHead(table, columns, sort, onSort);

  const visible = view.visibleTargets ?? snapshot.targets.filter((t) => matchesFilter(t, filter, meta.features));
  const focusId = visible.some(t => String(t.id) === focusedId) ? Number(focusedId)
    : visible.some(t => t.id === view.focusedId) ? view.focusedId
    : visible.some(t => t.id === selectedId) ? selectedId : visible[0]?.id;
  const sortedFlat = sort?.key && sort.key !== "config";
  const groupsById = sortedFlat && new Map(meta.groups?.flatMap(g => (g.target_ids ?? []).map(id => [id, g.name])) ?? []);
  const hostIndex = keys.indexOf("host");
  const rows = [];
  const targets = new Map();
  const groups = new Map();
  for (const section of buildSections(visible, sortedFlat ? [] : meta.groups)) {
    if (section.name != null) {
      const summary = groupSummary(section.targets);
      const values = [section.targets.length, columns.length, ...summary.flatMap(({ level, label }) => [level, label])];
      // A section's first member identifies it even if group names coincide.
      const id = section.targets[0].id;
      const old = previous?.groups.get(id);
      const node = old?.name === section.name && sameValues(old.values, values)
        ? old.node : groupRow(section.name, section.targets.length, summary, columns.length);
      groups.set(id, { node, name: section.name, values });
      rows.push(node);
    }
    for (const t of section.targets) {
      const old = previous?.onOpen === onOpen ? previous?.targets.get(t.id) : null;
      const entry = old ?? makeTargetRow(t.id, onOpen);
      const row = entry.node;
      const level = rowLevel(t);
      const selected = t.id === selectedId;
      if (entry.level !== level) row.className = `target-row health-${level}`;
      if (entry.host !== t.host) row.setAttribute("aria-label", `${t.host}, select to inspect`);
      if (entry.selected !== selected) {
        if (selected) row.setAttribute("aria-current", "true");
        else row.removeAttribute("aria-current");
      }
      const levels = cellLevels(t);
      const group = groupsById ? groupsById.get(t.id) : null;
      const cellValues = updateCells(row, columns,
        (key) => targetCellValues(key, t, levels, meta.features, group, sparks),
        targetCell, sameColumns ? entry.cellValues : undefined);
      while (row.cells.length > columns.length) row.deleteCell(-1);
      const button = row.cells[hostIndex].firstElementChild;
      const tabIndex = t.id === focusId ? 0 : -1;
      if (button.tabIndex !== tabIndex) button.tabIndex = tabIndex;
      targets.set(t.id, { node: row, host: t.host, level, selected, cellValues });
      rows.push(row);
    }
  }
  syncChildren(table.tBodies[0], rows);
  renderedTables.set(table, { keys, head, targets, groups, onOpen });

  if (focusedId && !table.contains(active)) table.querySelector(`.host-open[data-target-id="${CSS.escape(focusedId)}"]`)?.focus({ preventScroll: true });
  if (focusedSort && !table.contains(active)) table.querySelector(`[data-sort="${CSS.escape(focusedSort)}"]`)?.focus({ preventScroll: true });
  return visible.length;
}
