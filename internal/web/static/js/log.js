// The log is a global page: target selection and the monitor filter never
// change its contents. Fetch only while visible, retaining text and scroll.
import { el, fetchJSON } from "./dom.js";
import { formatTime } from "./time.js";

export function createLog(dom) {
  let active = false, pending = false, events = null, error = null;
  let version = 0, rendered = -1;
  const refresh = async () => {
    if (!active || pending || document.hidden || dom.isStopped?.()) return;
    pending = true;
    try {
      const result = await fetchJSON("api/v1/events?limit=200");
      if (error || JSON.stringify(events) !== JSON.stringify(result.events)) {
        events = result.events;
        error = null;
        version++;
      }
    } catch (err) {
      if (events === null) {
        error = err;
        version++;
      }
    } finally {
      pending = false;
      dom.requestRender();
    }
  };
  setInterval(refresh, 5000);
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
      dom.status.textContent = "All targets · latest 200 events · newest first";
      if (error) {
        dom.body.replaceChildren(el("p", { className: "empty", text: `Couldn't load events: ${error.message}` }));
        return;
      }
      if (events === null) return;
      const top = dom.body.scrollTop;
      const previous = dom.body.querySelector(".table-wrap");
      const left = previous?.scrollLeft ?? 0;
      const innerTop = previous?.scrollTop ?? 0;
      const body = el("tbody");
      for (const event of events) {
        body.append(el("tr", {},
          el("td", { className: "mono" }, el("time", { text: formatTime(event.at), attrs: { datetime: event.at } })),
          el("td", { className: "log-host", text: event.host }),
          el("td", { text: event.kind }),
          el("td", { className: "log-message", text: event.count > 1
            ? `${event.message} ×${event.count} (last ${formatTime(event.last_at)})` : event.message })));
      }
      const head = el("thead", {}, el("tr", {}, ...["Time", "Host", "Kind", "Message"].map(text => el("th", { text, attrs: { scope: "col" } }))));
      const cols = el("colgroup", {}, ...["112px", "220px", "140px", ""].map(width => {
        const col = el("col"); col.style.width = width; return col;
      }));
      dom.body.replaceChildren(events.length
        ? el("div", { className: "table-wrap" }, el("table", { className: "grid log" }, cols, head, body))
        : el("p", { className: "empty", text: "No events recorded." }));
      const wrap = dom.body.querySelector(".table-wrap");
      if (wrap) {
        wrap.scrollLeft = left;
        wrap.scrollTop = innerTop;
      }
      dom.body.scrollTop = top;
    },
  };
}
