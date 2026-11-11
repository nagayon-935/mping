// Summary chart window preference. Fetching samples and painting the chart
// remain separate so changing the window does not rebuild the workspace.
import { el, readSession, writeSession } from "./dom.js";
import { reconcileWindow } from "./timeline.js";

const WINDOW_KEY = "mping.window";
const MAX_HISTORY_POINTS = 3000; // the server's per-target RTT ring

export function createGraphTools(view, dom, { refreshHistory }) {
  return {
    updateWindows(intervalMs) {
      const preferred = view.window?.label ?? readSession(WINDOW_KEY);
      const { options, window } = reconcileWindow(preferred, intervalMs, MAX_HISTORY_POINTS);
      view.windows = options;
      view.window = window;
      dom.window.replaceChildren(...options.map((option) =>
        el("option", { text: option.label, attrs: {
          value: option.label, ...(option === window ? { selected: "" } : {}),
        } })));
    },

    install() {
      dom.window.addEventListener("change", (event) => {
        view.window = view.windows.find((option) => option.label === event.target.value) ?? view.window;
        writeSession(WINDOW_KEY, view.window.label);
        refreshHistory();
      });
    },
  };
}
