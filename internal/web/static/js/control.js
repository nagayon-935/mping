// Control session: the per-launch token, the /session check, authorised
// requests, and the two-step confirm used by destructive buttons.
import { el } from "./dom.js";
import { tokenFromHash } from "./model.js";

const TOKEN_KEY = "mping.token";

function readStored() {
  try {
    return localStorage.getItem(TOKEN_KEY);
  } catch {
    return null; // storage blocked: the token then lives for this page only
  }
}

function store(token) {
  try {
    if (token) localStorage.setItem(TOKEN_KEY, token);
    else localStorage.removeItem(TOKEN_KEY);
  } catch {
    // See readStored.
  }
}

/**
 * Takes a token from "#token=" (then strips it from the address bar and
 * history so it is not bookmarked or shared by accident), else from storage.
 * localStorage is per origin, i.e. per mping port.
 */
export function createControl() {
  let token = tokenFromHash(location.hash);
  if (token) {
    store(token);
    history.replaceState(null, "", location.pathname + location.search);
  } else {
    token = readStored();
  }
  let allowed = false;

  const request = async (method, path, body) => {
    const headers = { "X-Mping-Token": token ?? "" };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    let resp;
    try {
      resp = await fetch(path, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
    } catch (err) {
      return { ok: false, error: `mping is unreachable (${err.message})` };
    }
    if (resp.ok) return { ok: true };
    let error = `HTTP ${resp.status}`;
    try {
      error = (await resp.json()).error ?? error;
    } catch {
      // Non-JSON error body; keep the status line.
    }
    if (resp.status === 403) {
      // A stale token from an earlier mping run: fall back to read-only.
      allowed = false;
      store(null);
    }
    return { ok: false, error };
  };

  return {
    get allowed() { return allowed; },
    /** Asks the server whether this page may make changes. */
    async refresh() {
      if (!token) return (allowed = false);
      try {
        const resp = await fetch("api/v1/session", { headers: { "X-Mping-Token": token } });
        allowed = resp.ok && (await resp.json()).control === true;
      } catch {
        allowed = false;
      }
      return allowed;
    },
    addHost: (host) => request("POST", "api/v1/targets", { host }),
    deleteTarget: (id) => request("DELETE", `api/v1/targets/${id}`),
    reset: () => request("POST", "api/v1/reset"),
  };
}

/**
 * Two-step destructive button: the first press arms it ("Confirm …?") next
 * to a Cancel button; the second runs action. Disarms itself after 6s.
 * @param {string} label
 * @param {string} armedLabel
 * @param {() => Promise<void>} action
 */
export function confirmButton(label, armedLabel, action) {
  const button = el("button", { className: "btn btn-danger", text: label, attrs: { type: "button" } });
  const cancel = el("button", { className: "btn", text: "Cancel", attrs: { type: "button", hidden: "" } });
  let timer = null;
  const disarm = () => {
    clearTimeout(timer);
    button.textContent = label;
    button.dataset.armed = "";
    cancel.hidden = true;
  };
  button.addEventListener("click", async () => {
    if (!button.dataset.armed) {
      button.dataset.armed = "1";
      button.textContent = armedLabel;
      cancel.hidden = false;
      timer = setTimeout(disarm, 6000);
      return;
    }
    disarm();
    button.disabled = true;
    try {
      await action();
    } finally {
      button.disabled = false;
    }
  });
  cancel.addEventListener("click", () => {
    disarm();
    button.focus();
  });
  return el("span", { className: "confirm" }, button, cancel);
}
