// Control session: the per-launch token, the /session check, authorised
// requests, and in-place verification of pasted tokens.
import { el } from "./dom.js";
import { tokenFromHash } from "./model.js";

const TOKEN_KEY = "mping.token";

export function controlToken(raw, origin) {
  let value = String(raw ?? "").trim();
  if (/^https?:/i.test(value)) {
    try {
      const url = new URL(value);
      if (url.protocol !== "http:" || !["127.0.0.1", "localhost", "[::1]"].includes(url.hostname) ||
          (origin && url.port !== new URL(origin).port)) return null;
      value = tokenFromHash(url.hash) ?? "";
    } catch { return null; }
  }
  return /^[A-Za-z0-9._~-]{16,}$/.test(value) ? value : null;
}

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
  let revision = 0;

  const parseErrorResponse = async (resp) => {
    let error = `HTTP ${resp.status}`;
    try {
      error = (await resp.json()).error ?? error;
    } catch {
      // Non-JSON error body; keep the status line.
    }
    if (resp.status === 403) {
      // A stale token from an earlier mping run: fall back to read-only.
      allowed = false;
      token = null;
      revision++;
      store(null);
    }
    return { ok: false, error };
  };

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
    return parseErrorResponse(resp);
  };

  return {
    get allowed() { return allowed; },
    /** Asks the server whether this page may make changes. */
    async refresh() {
      if (!token) return (allowed = false);
      const version = revision;
      try {
        const resp = await fetch("api/v1/session", { headers: { "X-Mping-Token": token } });
        const valid = resp.ok && (await resp.json()).control === true;
        if (version !== revision) return allowed;
        allowed = valid;
        if (resp.ok && !valid) { token = null; store(null); }
      } catch {
        if (version === revision) allowed = false;
      }
      return allowed;
    },
    async authenticate(raw, signal) {
      const candidate = controlToken(raw, location.origin);
      if (!candidate) return { ok: false, error: "Paste the control token or the URL copied from mping's b menu (same port)." };
      const version = ++revision;
      try {
        const resp = await fetch("api/v1/session", { headers: { "X-Mping-Token": candidate }, signal });
        if (!resp.ok) return { ok: false, error: `Couldn't verify controls (HTTP ${resp.status}). Try again.` };
        const body = await resp.json();
        if (signal?.aborted || version !== revision) return { ok: false, cancelled: true };
        if (!body.control) return { ok: false, error: "Token not accepted by this mping session. Copy the current URL from the TUI's b menu." };
        token = candidate;
        allowed = true;
        store(token);
        return { ok: true };
      } catch {
        return signal?.aborted ? { ok: false, cancelled: true } : { ok: false, error: "Cannot reach mping. Check the connection and try again." };
      }
    },
    addHost: (host) => request("POST", "api/v1/targets", { host }),
    deleteTarget: (id) => request("DELETE", `api/v1/targets/${id}`),
    reset: () => request("POST", "api/v1/reset"),
    stop: () => request("POST", "api/v1/stop"),
    restart: () => request("POST", "api/v1/restart"),
    async downloadReport(format, filename, { signal } = {}) {
      try {
        const response = await fetch(`api/v1/report?format=${format}`, { headers: { "X-Mping-Token": token ?? "" }, signal });
        if (!response.ok) {
          return parseErrorResponse(response);
        }
        const blob = await response.blob();
        if (signal?.aborted) return { ok: false, cancelled: true };
        const url = URL.createObjectURL(blob);
        const link = el("a", { attrs: { href: url, download: filename } });
        document.body.append(link);
        link.click();
        link.remove();
        setTimeout(() => URL.revokeObjectURL(url), 1000);
        return { ok: true };
      } catch (err) {
        if (signal?.aborted) return { ok: false, cancelled: true };
        return { ok: false, error: `Couldn't download report: ${err.message}` };
      }
    },
  };
}
