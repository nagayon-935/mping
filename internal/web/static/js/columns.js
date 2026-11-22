import { el } from "./dom.js";
import { formatCount, formatRTT } from "./model.js";

// Shared width contracts. Numeric columns reserve the entire formatted value,
// including its unit; the identity column receives the remaining table width.
const widths = {
  status: "var(--col-status)", host: "var(--col-host)", ip: "var(--col-ip)",
  asn: "var(--col-asn)", loss: "var(--col-loss)", hopLoss: "var(--col-hop-loss)",
  sent: "var(--col-count)", recv: "var(--col-count)", count: "var(--col-count)",
  last: "var(--col-rtt)", avg: "var(--col-rtt)", min: "var(--col-rtt)",
  max: "var(--col-rtt)", jitter: "var(--col-rtt)", rtt: "var(--col-rtt)",
  ttl: "var(--col-ttl)", spark: "var(--col-spark)",
  url: "var(--col-url)", code: "var(--col-code)", port: "var(--col-port)",
  statusLabel: "var(--col-status-label)",
};

/** Width in pixels of a column's CSS contract, as resolved for this table. */
export function columnWidthPx(table, key) {
  const name = /var\((--[\w-]+)\)/.exec(widths[key])?.[1];
  return name ? parseFloat(getComputedStyle(table).getPropertyValue(name)) || 0 : 0;
}

export function setColumns(table, columns, flexibleKey) {
  const group = elGroup(columns, flexibleKey);
  table.querySelector("colgroup")?.remove();
  table.insertBefore(group, table.tHead);
  const total = columns.map((c) => widths[c.key]).join(" + ");
  table.style.minWidth = `calc(${total})`;
  table.tHead.replaceChildren(el("tr", {}, ...columns.map((c) => el("th", {
    className: `${c.num ? "num " : ""}col-${c.key}`,
    text: c.label,
    attrs: { scope: "col", ...(c.title ? { title: c.title } : {}) },
  }))));
}

function elGroup(columns, flexibleKey) {
  return el("colgroup", {}, ...columns.map((c) => {
    const col = el("col", { className: `col-${c.key}` });
    if (c.key !== flexibleKey) col.style.width = widths[c.key];
    return col;
  }));
}

// Table cells shared by the ping monitor and the inspect pane's tables.
// Abbreviated values carry the exact one in the title (hover).

/** Text that may be cut with an ellipsis; the full text stays in the title. */
export function textCell(text, className = "") {
  return el("td", { className, attrs: { title: text } },
    el("span", { className: "cell-text", text }));
}

/** Right-aligned number, tinted when its level is warn or crit. */
export function numCell(text, level) {
  const td = el("td", { className: "num", text });
  if (level === "warn" || level === "crit") td.classList.add(`lvl-${level}`);
  return td;
}

export function countCell(value) {
  const cell = numCell(formatCount(value));
  cell.title = value.toLocaleString("en-US");
  return cell;
}

export function rttCell(value, level) {
  const cell = numCell(formatRTT(value), level);
  cell.title = formatRTT(value, false);
  return cell;
}
