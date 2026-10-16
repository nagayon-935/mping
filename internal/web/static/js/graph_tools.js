// Graph window and scale preferences. Fetching samples and painting charts
// remain separate so changing a control does not rebuild the workspace.
import { el, readSession, writeSession } from "./dom.js";
import { reconcileWindow } from "./timeline.js";

const WINDOW_KEY = "mping.window";
const SCALE_KEY = "mping.sharedScale";
const MAX_HISTORY_POINTS = 3000; // the server's per-target RTT ring

export function createGraphTools(view, dom, { requestRender, refreshHistory }) {
  function setShared(shared) {
    view.shared = shared;
    writeSession(SCALE_KEY, String(shared));
    dom.perTarget.setAttribute("aria-pressed", String(!shared));
    dom.shared.setAttribute("aria-pressed", String(shared));
    requestRender();
  }

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
      dom.perTarget.addEventListener("click", () => setShared(false));
      dom.shared.addEventListener("click", () => setShared(true));
      setShared(readSession(SCALE_KEY) !== "false");
    },
  };
}
