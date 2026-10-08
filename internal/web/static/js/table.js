// Target and HTTP-check tables. Rebuilt from the snapshot on every update;
// row focus and selection are restored by target ID.
import { el, statusChip } from "./dom.js";
import { drawSparkline } from "./chart.js";
import {
  buildSections, cellLevels, dscpName, formatMs, formatPct, lossRate, matchesFilter, rowLevel,
} from "./model.js";

const levelLabels = { ok: "OK", warn: "Warn", crit: "Crit", pending: "Waiting" };

/** Column set, following the TUI: optional columns only when the feature is on. */
function targetColumns(features) {
  return [
    { key: "status", label: "Status" },
    { key: "host", label: "Host" },
    { key: "ip", label: "IP" },
    features.asn && { key: "asn", label: "AS" },
    { key: "loss", label: "Loss %", num: true },
    { key: "sent", label: "Sent", num: true },
    { key: "recv", label: "Recv", num: true },
    { key: "last", label: "Last ms", num: true },
    { key: "avg", label: "Avg ms", num: true },
    { key: "min", label: "Min ms", num: true },
    { key: "max", label: "Max ms", num: true },
    { key: "jitter", label: "Jitter ms", num: true },
    { key: "ttl", label: "TTL", num: true },
    features.dscp && { key: "dscp", label: "DSCP" },
    { key: "spark", label: "RTT trend" },
  ].filter(Boolean);
}

function hostCell(t) {
  const td = el("td", { className: "host-cell" }, el("span", { className: "host", text: t.host }));
  if (t.ptr && t.ptr !== t.host) td.append(el("span", { className: "sub", text: t.ptr }));
  return td;
}

function numCell(text, level) {
  const td = el("td", { className: "num", text });
  if (level === "warn" || level === "crit") td.classList.add(`lvl-${level}`);
  return td;
}

function targetCell(col, t, th, cells, sparks) {
  switch (col.key) {
    case "status": {
      const level = rowLevel(t, th);
      return el("td", {}, statusChip(level, levelLabels[level]));
    }
    case "host": return hostCell(t);
    case "ip": return el("td", { className: "mono", text: t.ip || "–" });
    case "asn": return el("td", { text: [t.asn, t.org].filter(Boolean).join(" ") || "–" });
    case "loss": return numCell(t.recv + t.loss === 0 ? "–" : formatPct(lossRate(t)), cells.loss);
    case "sent": return numCell(String(t.sent));
    case "recv": return numCell(String(t.recv));
    case "last": return numCell(formatMs(t.last_rtt_ms), cells.last);
    case "avg": return numCell(formatMs(t.avg_rtt_ms), cells.avg);
    case "min": return numCell(formatMs(t.min_rtt_ms), cells.min);
    case "max": return numCell(formatMs(t.max_rtt_ms), cells.max);
    case "jitter": return numCell(formatMs(t.jitter_ms), cells.jitter);
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

function colClass(c) {
  return [c.num && "num", c.key && `col-${c.key}`].filter(Boolean).join(" ");
}

function setHeader(table, columns) {
  const row = el("tr");
  for (const c of columns) {
    row.append(el("th", { className: colClass(c), text: c.label, attrs: { scope: "col" } }));
  }
  table.tHead.replaceChildren(row);
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
  const columns = targetColumns(meta.features);
  setHeader(table, columns);

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
          "aria-selected": String(t.id === selectedId),
          "aria-label": `${t.host}, details`,
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
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          onOpen(t.id);
        }
      });
      rows.push(tr);
    }
  }
  table.tBodies[0].replaceChildren(...rows);

  for (const [canvas, id] of sparks) drawSparkline(canvas, history.get(id));
  if (focusedId) table.querySelector(`tr[data-target-id="${CSS.escape(focusedId)}"]`)?.focus();
  return visible.length;
}

const httpLevels = { Up: "ok", Down: "crit", Error: "crit" };

/** @returns {boolean} whether any HTTP checks exist */
export function renderHTTP(table, checks) {
  if (!checks || checks.length === 0) return false;
  setHeader(table, [
    { label: "Status" }, { label: "URL" }, { label: "Code", num: true },
    { label: "Last ms", num: true }, { label: "Avg ms", num: true },
    { label: "Min ms", num: true }, { label: "Max ms", num: true },
    { label: "Up", num: true }, { label: "Down", num: true },
  ]);
  const rows = checks.map((c) => el("tr", {},
    el("td", {}, statusChip(httpLevels[c.status] ?? "pending", c.status || "Waiting")),
    el("td", { className: "mono", text: c.url }),
    numCell(c.status_code > 0 ? String(c.status_code) : "–", c.status_code >= 500 ? "crit" : c.status_code >= 300 ? "warn" : "none"),
    numCell(formatMs(c.last_rtt_ms)), numCell(formatMs(c.avg_rtt_ms)),
    numCell(formatMs(c.min_rtt_ms)), numCell(formatMs(c.max_rtt_ms)),
    numCell(String(c.up_count)), numCell(String(c.down_count))));
  table.tBodies[0].replaceChildren(...rows);
  return true;
}
