import { el, statusChip } from "./dom.js";
import { countCell, numCell, rttCell, textCell } from "./cells.js";
import { setColumns } from "./columns.js";

/** headers: [label, numeric, column key]; flexibleKey: the column that takes spare width. */
function simpleTable(headers, rows, flexibleKey) {
  const columns = headers.map(([label, num, key]) => ({ label, num, key }));
  const body = el("tbody");
  for (const cells of rows) {
    cells.forEach((cell, i) => cell.classList.add(`col-${columns[i].key}`));
    body.append(el("tr", {}, ...cells));
  }
  const table = el("table", { className: "grid" }, el("thead"), body);
  setColumns(table, columns, flexibleKey);
  return el("div", { className: "table-wrap" }, table);
}

export function portsFor(t) {
  if (!t.port_results?.length) return el("p", { className: "empty", text: "No port checks for this target." });
  return simpleTable(
    [["Port", false, "port"], ["Status", false, "statusLabel"], ["RTT", true, "rtt"], ["Open", true, "count"], ["Closed", true, "count"]],
    t.port_results.map((p) => [
      textCell(`${p.port}/${p.protocol}`, "mono"),
      el("td", {}, statusChip(p.level ?? "pending", p.status || "Waiting")),
      rttCell(p.rtt_ms), countCell(p.open_count), countCell(p.closed_count),
    ]),
  );
}

export function httpFor(checks) {
  if (!checks?.length) return el("p", { className: "empty", text: "No HTTP checks." });
  return simpleTable(
    [["Status", false, "statusLabel"], ["URL", false, "url"], ["Code", true, "code"], ["Last", true, "last"], ["Avg", true, "avg"],
      ["Min", true, "min"], ["Max", true, "max"], ["Up", true, "sent"], ["Down", true, "recv"]],
    checks.map((c) => [
      el("td", {}, statusChip(c.level ?? "pending", c.status || "Waiting")),
      textCell(c.url, "mono"),
      numCell(c.status_code > 0 ? String(c.status_code) : "–", c.level === "ok" ? "none" : c.level ?? "none"),
      rttCell(c.last_rtt_ms), rttCell(c.avg_rtt_ms), rttCell(c.min_rtt_ms), rttCell(c.max_rtt_ms),
      countCell(c.up_count), countCell(c.down_count),
    ]),
    "url",
  );
}
