// Activity: the detail area while no host is selected. Current state and how
// long it has lasted, a summary of the last hour of health events, and the
// newest events across all hosts. No chart: rows carry sparklines and the
// selected host's Summary has the large chart.
import { badgeIcon, el, fetchJSON, statusChip } from "./dom.js";
import { STATUS_LABELS } from "./model.js";
import { formatTime } from "./time.js";
import { activityItems, formatDuration, lastHourSummary, recentAcrossHosts } from "./activity_model.js";

/** "▲ host" with the level spelled out for screen readers. */
function whoChip(level, who) {
  return el("span", { className: `status lvl-${level}` },
    el("span", { className: "status-icon", text: badgeIcon(level), attrs: { "aria-hidden": "true" } }),
    el("span", { className: "visually-hidden", text: `${STATUS_LABELS[level] ?? level}: ` }),
    el("span", { text: who }));
}

const EVENTS_LIMIT = 1000; // the server's cap for /api/v1/events
const REFRESH_MS = 10000;

function heading(text) {
  return el("h3", { text });
}

/** Status, host (selects it), reason and duration for one host. */
function stateItem(item, onSelect) {
  const { target, level, reason, durationMs } = item;
  const select = el("button", {
    className: "link-button", text: target.host,
    attrs: { type: "button", "data-target-id": String(target.id), "aria-label": `Show Summary for ${target.host}` },
  });
  select.addEventListener("click", () => onSelect(target.id));
  const duration = formatDuration(durationMs);
  return el("li", { className: `activity-item lvl-${level}` },
    statusChip(level),
    el("span", { className: "activity-host" }, select, reason ? el("span", { className: "activity-reason", text: ` — ${reason}` }) : null),
    el("span", { className: "activity-duration", text: duration === "–" ? "" : `for ${duration}`,
      attrs: { title: target.health?.since ? `Since ${formatTime(target.health.since)}` : "" } }));
}

function statesSection(targets, mode, onSelect) {
  const { items, okCount } = activityItems(targets, { mode });
  const children = [heading(mode === "attention" ? "Needs attention" : "Current state")];
  if (items.length) children.push(el("ul", { className: "activity-states" }, ...items.map((i) => stateItem(i, onSelect))));
  if (mode === "attention") {
    children.push(el("p", { className: "pane-hint", text: items.length
      ? `${okCount} other ${okCount === 1 ? "host" : "hosts"} OK`
      : `All ${okCount} ${okCount === 1 ? "host is" : "hosts are"} OK` }));
  }
  return el("section", { className: "inspect-section" }, ...children);
}

function lastHourSection(events, error) {
  const section = el("section", { className: "inspect-section" }, heading("Last hour"));
  if (error || !events) {
    section.append(el("p", { className: "pane-hint", text: error ?? "Loading…" }));
    return section;
  }
  const s = lastHourSummary(events, { limit: EVENTS_LIMIT });
  const atLeast = s.partial ? "at least " : "";
  const parts = [["crit", "Critical"], ["warn", "Warning"], ["ok", "recovered"], ["pending", "waiting"]]
    .filter(([k]) => s.changes[k] > 0).map(([k, label]) => `${s.changes[k]} ${label}`);
  const pairs = [
    ["State changes", s.changes.total === 0 ? "None" : `${atLeast}${s.changes.total} (${parts.join(" · ")})`],
    ["Least stable", s.leastStable ? `${s.leastStable.host} — ${s.leastStable.count} changes` : "–"],
    ["Simultaneous", s.simultaneous.length
      ? s.simultaneous.slice(0, 3).map((c) => `${c.metric} on ${c.hosts} hosts at ${formatTime(c.at)}`).join(" · ")
      : "None"],
  ];
  const dl = el("dl", { className: "facts" });
  for (const [k, v] of pairs) dl.append(el("dt", { text: k }), el("dd", { text: v }));
  section.append(dl);
  if (s.partial) section.append(el("p", { className: "chart-caption", text: "Older events were not retained, so counts may be low." }));
  section.querySelector("dt:nth-of-type(3)").title = "At least 3 hosts started breaching the same metric within 5 seconds.";
  return section;
}

function recentSection(events, error) {
  const section = el("section", { className: "inspect-section" }, heading("Recent events (all hosts)"));
  if (error || !events) {
    section.append(el("p", { className: "pane-hint", text: error ?? "Loading…" }));
  } else {
    const recent = recentAcrossHosts(events);
    section.append(recent.length
      ? el("ol", { className: "event-list activity-events" }, ...recent.map((e) => el("li", {},
        el("time", { className: "event-time", text: formatTime(e.at), attrs: { datetime: e.at } }),
        el("span", { className: "event-who" },
          e.level ? whoChip(e.level, e.hosts.length > 1 ? `${e.hosts.length} hosts` : e.hosts[0])
            : el("span", { className: "event-kind-label", text: `${e.hosts.length > 1 ? `${e.hosts.length} hosts` : e.hosts[0]} · ${e.kind}` })),
        el("span", { className: "event-text", text: e.text, attrs: e.hosts.length > 1 ? { title: e.hosts.join(", ") } : {} }))))
      : el("p", { className: "pane-hint", text: "No events recorded." }));
  }
  section.append(el("a", { className: "chart-scale", text: "View all in Log", attrs: { href: "#log" } }));
  return section;
}

/**
 * @param {{body: HTMLElement, onSelect: (id: number) => void, isStopped: () => boolean, requestRender: () => void}} dom
 */
export function createActivity(dom) {
  let active = false;
  let events = null;
  let error = null;
  let pending = false;

  const refresh = async () => {
    if (!active || pending || document.hidden) return;
    if (dom.isStopped()) {
      if (!events) error = "Session ended — events are not available.";
      dom.requestRender();
      return;
    }
    pending = true;
    try {
      events = (await fetchJSON(`api/v1/events?limit=${EVENTS_LIMIT}`)).events ?? [];
      error = null;
    } catch (err) {
      if (!events) error = `Couldn't load events: ${err.message}`;
    } finally {
      pending = false;
      dom.requestRender();
    }
  };
  setInterval(refresh, REFRESH_MS);
  document.addEventListener("visibilitychange", refresh);

  return {
    /** Fetch only while the panel is shown. */
    activate(on) {
      const opening = on && !active;
      active = on;
      if (opening) refresh();
    },
    /**
     * @param {object[]} targets every host (filters do not apply)
     * @param {"all"|"attention"} mode
     * @param {boolean} wide two columns when the panel spans the page
     */
    render(targets, mode, wide) {
      const focused = dom.body.contains(document.activeElement) ? document.activeElement.dataset?.targetId : null;
      const top = dom.body.scrollTop;
      const left = el("div", { className: "activity-col" }, statesSection(targets, mode, dom.onSelect), lastHourSection(events, error));
      const right = el("div", { className: "activity-col" }, recentSection(events, error));
      dom.body.replaceChildren(el("div", { className: `activity-grid${wide ? " is-wide" : ""}` }, left, right));
      dom.body.scrollTop = top;
      if (focused) dom.body.querySelector(`[data-target-id="${CSS.escape(focused)}"]`)?.focus({ preventScroll: true });
    },
  };
}
