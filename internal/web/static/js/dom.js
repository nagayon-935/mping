// Tiny DOM helpers. Every piece of data goes in through textContent: host
// names, PTR records, AS org names and event messages can come from DNS or
// remote systems and must never be parsed as HTML.

/**
 * @param {string} tag
 * @param {{className?: string, text?: string, attrs?: Record<string, string>}} [opts]
 * @param {...Node} children
 */
export function el(tag, opts = {}, ...children) {
  const node = document.createElement(tag);
  if (opts.className) node.className = opts.className;
  if (opts.text != null) node.textContent = opts.text;
  for (const [k, v] of Object.entries(opts.attrs ?? {})) node.setAttribute(k, v);
  for (const c of children) if (c) node.append(c);
  return node;
}

const icons = { ok: "●", warn: "▲", crit: "■", pending: "○", none: "○", stopped: "◼" };
const labels = { ok: "OK", warn: "Warn", crit: "Crit", pending: "Waiting", none: "–" };

/** Status as shape + label so it never relies on colour alone. */
export function statusChip(level, label = labels[level]) {
  return el("span", { className: `status lvl-${level}` },
    el("span", { className: "status-icon", text: icons[level] ?? "○", attrs: { "aria-hidden": "true" } }),
    el("span", { text: label }));
}

export function badgeIcon(level) {
  return icons[level] ?? "○";
}

/** Reads a CSS custom property from :root (theme-aware chart colours). */
export function cssVar(name) {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

export async function fetchJSON(url, signal) {
  const resp = await fetch(url, { headers: { Accept: "application/json" }, signal });
  if (!resp.ok) throw new Error(`${url}: HTTP ${resp.status}`);
  return resp.json();
}
