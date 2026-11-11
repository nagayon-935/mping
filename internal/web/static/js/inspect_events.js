// "Recent events" in Summary: the selected host's latest events (the same
// records the Log page lists for every host), refreshed while Summary is open.
import { el, fetchJSON, statusChip } from "./dom.js";
import { formatTime } from "./time.js";

export const RECENT_EVENTS = 6;
const REFRESH_MS = 5000;
const HEALTH = /^(ok|warn|crit|pending): (.*)$/;

/** A health transition ("crit: Jitter 50.5ms > 50.0ms") as level + text. */
export function describeEvent(event) {
  const m = event.kind === "health" ? HEALTH.exec(event.message ?? "") : null;
  const message = m ? m[2] : event.message;
  return {
    level: m ? m[1] : null,
    kind: event.kind,
    text: event.count > 1 ? `${message} ×${event.count} (last ${formatTime(event.last_at)})` : message,
  };
}

/** Newest first, at most `limit`, from the server's oldest-first list. */
export function latestEvents(events, limit = RECENT_EVENTS) {
  return [...(events ?? [])].sort((a, b) => Date.parse(b.last_at ?? b.at) - Date.parse(a.last_at ?? a.at)).slice(0, limit);
}

export function createRecentEvents({ isStopped }) {
  const list = el("ol", { className: "event-list" });
  const status = el("p", { className: "pane-hint", text: "Loading…" });
  const node = el("section", { className: "inspect-section", attrs: { "data-section": "events" } },
    el("div", { className: "chart-head" }, el("h3", { text: "Recent events" }), el("span", { className: "spacer" }),
      el("a", { className: "chart-scale", text: "All events in Log", attrs: { href: "#log" } })),
    status, list);
  let targetId = null;
  let rendered = "";
  let pending = false;

  const draw = (events) => {
    const key = JSON.stringify(events);
    if (key === rendered) return;
    rendered = key;
    const shown = latestEvents(events);
    status.hidden = shown.length > 0;
    status.textContent = "No events recorded for this host.";
    list.replaceChildren(...shown.map((event) => {
      const d = describeEvent(event);
      return el("li", {},
        el("time", { className: "event-time", text: formatTime(event.at), attrs: { datetime: event.at } }),
        el("span", { className: "event-kind" }, d.level ? statusChip(d.level) : el("span", { text: d.kind })),
        el("span", { className: "event-text", text: d.text }));
    }));
  };

  const refresh = async () => {
    const id = targetId;
    if (id == null) return;
    if (isStopped()) {
      if (rendered === "") {
        status.hidden = false;
        status.textContent = "Session ended — events are not available.";
      }
      return;
    }
    if (pending || document.hidden) return;
    pending = true;
    try {
      const body = await fetchJSON(`api/v1/targets/${encodeURIComponent(id)}/events`);
      if (id === targetId) draw(body.events);
    } catch (err) {
      if (id === targetId && rendered === "") {
        status.hidden = false;
        status.textContent = `Couldn't load events: ${err.message}`;
      }
    } finally {
      pending = false;
      if (targetId != null && targetId !== id) refresh();
    }
  };
  setInterval(refresh, REFRESH_MS);
  // A host selected while the tab was hidden loads as soon as it is shown.
  document.addEventListener("visibilitychange", refresh);

  return {
    node,
    /** Follow a target (null when Summary is closed). */
    show(id) {
      if (id === targetId) return;
      targetId = id;
      rendered = "";
      list.replaceChildren();
      status.hidden = false;
      status.textContent = "Loading…";
      refresh();
    },
  };
}
