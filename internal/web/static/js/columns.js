// Shared width contracts. Numeric columns reserve the entire formatted value,
// including its unit; the identity column receives the remaining table width.
const widths = {
  status: "var(--col-status)", host: "var(--col-host)", ip: "var(--col-ip)",
  asn: "var(--col-asn)", loss: "var(--col-loss)", hopLoss: "var(--col-hop-loss)",
  sent: "var(--col-count)", recv: "var(--col-count)", count: "var(--col-count)",
  last: "var(--col-rtt)", avg: "var(--col-rtt)", min: "var(--col-rtt)",
  max: "var(--col-rtt)", jitter: "var(--col-rtt)", rtt: "var(--col-rtt)",
  ttl: "var(--col-ttl)", dscp: "var(--col-dscp)", spark: "var(--col-spark)",
  url: "var(--col-url)", code: "var(--col-code)", port: "var(--col-port)",
  statusLabel: "var(--col-status-label)",
};

/** Width in pixels of a column's CSS contract, as resolved for this table. */
export function columnWidthPx(table, key) {
  const name = /var\((--[\w-]+)\)/.exec(widths[key])?.[1];
  return name ? parseFloat(getComputedStyle(table).getPropertyValue(name)) || 0 : 0;
}

/** A pixel-valued CSS custom property (e.g. "--host-grow") as a number. */
export function cssPx(table, name) {
  return parseFloat(getComputedStyle(table).getPropertyValue(name)) || 0;
}

export function setColumns(table, columns, flexibleKey) {
  const group = elGroup(columns, flexibleKey);
  table.querySelector("colgroup")?.remove();
  table.prepend(group);
  const total = columns.map((c) => widths[c.key]).join(" + ");
  table.style.minWidth = `calc(${total})`;
  // A flexible host column may only grow by --host-grow beyond its minimum;
  // spare width stays empty rather than stretching names across the page.
  table.style.maxWidth = flexibleKey === "host" ? `calc(${total} + var(--host-grow))` : "";
  const row = document.createElement("tr");
  for (const c of columns) {
    const th = document.createElement("th");
    th.className = `${c.num ? "num " : ""}col-${c.key}`;
    th.textContent = c.label;
    th.scope = "col";
    if (c.title) th.title = c.title;
    row.append(th);
  }
  table.tHead.replaceChildren(row);
}

function elGroup(columns, flexibleKey) {
  const group = document.createElement("colgroup");
  for (const c of columns) {
    const col = document.createElement("col");
    col.className = `col-${c.key}`;
    if (c.key !== flexibleKey) col.style.width = widths[c.key];
    group.append(col);
  }
  return group;
}
