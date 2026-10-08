// Table cells shared by the ping monitor and the inspect pane's tables.
// Abbreviated values carry the exact one in the title (hover).
import { el } from "./dom.js";
import { formatCount, formatRTT } from "./model.js";

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
