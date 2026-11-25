// The log fetches the latest events across hosts while visible.
import { el, fetchJSON, statusChip, statusLabel } from "./dom.js";
import { healthTransition } from "./health_text.js";
import { formatEventMessage, formatTime } from "./time.js";

const LIMIT = 200;

export function filterEvents(events, kind, host) {
  const query = host.trim().toLowerCase();
  return events.filter(event => (!kind || event.kind === kind) && event.host.toLowerCase().includes(query));
}

export function eventKinds(events, selected) {
  const kinds = new Set(events.map(event => event.kind).filter(Boolean));
  if (selected) kinds.add(selected);
  kinds.delete("health");
  return ["", "health", ...[...kinds].sort()];
}

export function kindLabel(kind) {
  if (!kind) return "All events";
  if (kind === "health") return "Health changes only";
  if (kind === "dns") return "DNS";
  return kind[0].toUpperCase() + kind.slice(1);
}

function eventKindChip(level) {
  const chip = statusChip(level, "health");
  chip.setAttribute("role", "img");
  chip.setAttribute("aria-label", `Health: ${statusLabel(level)}`);
  return chip;
}

export function createLog(dom) {
  let active = false, pending = false, events = null, error = null;
  let version = 0, rendered = -1, optionsKey;
  let kind = "", host = "";
  dom.host.value = host;
  function updateFilters() {
    dom.clear.hidden = !kind && !host;
    version++;
    dom.requestRender();
  }
  dom.kind.addEventListener("change", () => {
    kind = dom.kind.value;
    updateFilters();
  });
  dom.host.addEventListener("input", () => {
    host = dom.host.value;
    updateFilters();
  });
  dom.clear.addEventListener("click", () => {
    kind = dom.kind.value = "";
    host = dom.host.value = "";
    dom.host.focus();
    updateFilters();
  });
  const refresh = async () => {
    if (!active || pending || document.hidden) return;
    if (dom.isStopped?.()) {
      if (events === null && !error) {
        error = "Session ended — events are not available.";
        version++;
        dom.requestRender();
      }
      return;
    }
    pending = true;
    try {
      const result = await fetchJSON(`api/v1/events?limit=${LIMIT}`);
      if (error || JSON.stringify(events) !== JSON.stringify(result.events)) {
        events = result.events;
        error = null;
        version++;
      }
    } catch (err) {
      if (events === null) {
        error = `Couldn't load events: ${err.message}`;
        version++;
      }
    } finally {
      pending = false;
      dom.requestRender();
    }
  };
  setInterval(refresh, 5000);
  document.addEventListener("visibilitychange", refresh);
  return {
    activate(on) {
      const opening = on && !active;
      active = on;
      if (opening) refresh();
    },
    refresh,
    render() {
      if (!active || rendered === version) return;
      rendered = version;
      const kinds = eventKinds(events ?? [], kind);
      const key = kinds.join(",");
      if (optionsKey !== key) {
        optionsKey = key;
        dom.kind.replaceChildren(...kinds.map(value => el("option", { text: kindLabel(value), attrs: { value } })));
      }
      dom.kind.value = kind;
      dom.status.textContent = `Latest ${LIMIT} events across all targets · newest first`;
      if (error) {
        dom.body.replaceChildren(el("p", { className: "empty", text: error }));
        return;
      }
      if (events === null) return;
      const shown = filterEvents(events, kind, host);
      dom.status.textContent = `Showing ${shown.length} of ${events.length} events · newest first`;
      const top = dom.body.scrollTop;
      const previous = dom.body.querySelector(".table-wrap");
      const left = previous?.scrollLeft ?? 0;
      const innerTop = previous?.scrollTop ?? 0;
      const body = el("tbody");
      for (const event of shown) {
        const health = healthTransition(event);
        body.append(el("tr", { className: health ? `lvl-${health.level}` : "" },
          el("td", { className: "mono" }, el("time", { text: formatTime(event.at), attrs: { datetime: event.at } })),
          el("td", { className: "log-host", text: event.host }),
          health ? el("td", {}, eventKindChip(health.level)) : el("td", { text: event.kind }),
          el("td", { className: "log-message", text: formatEventMessage(event) })));
      }
      const head = el("thead", {}, el("tr", {}, ...["Time", "Host", "Kind", "Message"].map(text => el("th", { text, attrs: { scope: "col" } }))));
      const cols = el("colgroup", {}, ...["112px", "220px", "140px", ""].map(width => {
        const col = el("col"); col.style.width = width; return col;
      }));
      dom.body.replaceChildren(shown.length
        ? el("div", { className: "table-wrap" }, el("table", { className: "grid log" }, cols, head, body))
        : el("p", { className: "empty", text: events.length ? "No events match these filters." : "No events recorded." }));
      const wrap = dom.body.querySelector(".table-wrap");
      if (wrap) {
        wrap.scrollLeft = left;
        wrap.scrollTop = innerTop;
      }
      dom.body.scrollTop = top;
    },
  };
}
