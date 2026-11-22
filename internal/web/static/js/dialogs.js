import { validateHost } from "./model.js";

// Transient message below the header. Successes fade on their own; errors
// stay (and are announced assertively) until dismissed.
const HIDE_AFTER_MS = 4000;

/**
 * @param {{node: HTMLElement, text: HTMLElement, dismiss: HTMLButtonElement}} dom
 * @returns {(message: string, isError?: boolean) => void}
 */
export function createNotice(dom) {
  let timer = null;
  const hide = () => {
    clearTimeout(timer);
    dom.node.hidden = true;
    dom.text.textContent = "";
  };
  dom.dismiss.addEventListener("click", hide);

  return function notify(message, isError = false) {
    clearTimeout(timer);
    dom.text.setAttribute("role", isError ? "alert" : "status");
    dom.text.setAttribute("aria-live", isError ? "assertive" : "polite");
    dom.text.textContent = message;
    dom.node.hidden = false;
    dom.node.classList.toggle("is-error", isError);
    if (!isError) timer = setTimeout(hide, HIDE_AFTER_MS);
  };
}

// Confirmation for one irreversible control action (delete, stop...).
export function createConfirmation(dom) {
  let action = null, running = false;
  const close = () => { if (!running) dom.dialog.close(); };
  dom.cancel.addEventListener("click", close);
  dom.dialog.addEventListener("cancel", event => { if (running) event.preventDefault(); });
  dom.dialog.addEventListener("close", () => { action = null; });
  dom.form.addEventListener("submit", async event => {
    event.preventDefault();
    if (!action || running) return;
    running = true;
    dom.submit.disabled = dom.cancel.disabled = true;
    const label = dom.submit.textContent;
    dom.submit.textContent = "Working…";
    try {
      const result = await action();
      if (result.ok) dom.dialog.close();
      else { dom.error.textContent = result.error; dom.error.hidden = false; }
    } catch {
      dom.error.textContent = "Operation failed. Try again.";
      dom.error.hidden = false;
    } finally {
      running = false;
      dom.submit.disabled = dom.cancel.disabled = false;
      dom.submit.textContent = label;
    }
  });
  return {
    open({ title, description, label, run }) {
      if (running || dom.dialog.open) return;
      action = run;
      dom.title.textContent = title;
      dom.description.textContent = description;
      dom.submit.textContent = label;
      dom.error.hidden = true;
      dom.error.textContent = "";
      dom.dialog.showModal();
      dom.cancel.focus();
    },
  };
}

// Dialogs that need the control session: enabling controls with a pasted
// token or URL, and adding a host. Both stay open on failure and show the
// error inline so the user can correct their input.

const AUTH_HELP = "Press b in mping's TUI and choose Copy URL, then paste it below. " +
  "You can also paste MPING_WEB_TOKEN for a headless run. Controls are verified without reloading this page.";

/** Shows or clears an inline error and the matching aria-invalid state. */
function fieldError(error, input, message) {
  error.textContent = message ?? "";
  error.hidden = !message;
  if (message) input.setAttribute("aria-invalid", "true");
  else input.removeAttribute("aria-invalid");
}

/**
 * @param {object} dom auth dialog elements
 * @param {{control: object, onEnabled: () => void}} deps
 */
export function createAuthDialog(dom, { control, onEnabled }) {
  let pending = null; // AbortController of the in-flight verification
  const abort = () => pending?.abort();

  dom.explanation.textContent = AUTH_HELP;
  dom.dialog.addEventListener("cancel", abort);
  dom.dialog.addEventListener("close", () => { abort(); dom.token.value = ""; });
  dom.show.addEventListener("change", () => { dom.token.type = dom.show.checked ? "text" : "password"; });
  dom.token.addEventListener("input", () => fieldError(dom.error, dom.token));
  dom.form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (pending) return;
    const controller = new AbortController();
    pending = controller;
    dom.submit.disabled = true;
    dom.submit.textContent = "Verifying…";
    try {
      const result = await control.authenticate(dom.token.value, controller.signal);
      if (controller.signal.aborted) return;
      if (result.ok) {
        dom.dialog.close();
        onEnabled();
      } else {
        fieldError(dom.error, dom.token, result.error);
        dom.token.focus();
      }
    } finally {
      pending = null;
      dom.submit.disabled = false;
      dom.submit.textContent = "Enable controls";
    }
  });

  return {
    open() {
      if (control.allowed) return;
      dom.token.value = "";
      dom.token.type = "password";
      dom.show.checked = false;
      fieldError(dom.error, dom.token);
      dom.dialog.showModal();
    },
  };
}

/**
 * @param {object} dom add-host dialog elements
 * @param {{control: object, notify: (message: string, isError?: boolean) => void,
 *   onChange: () => void, requestAuth: () => void}} deps
 */
export function createAddDialog(dom, { control, notify, onChange, requestAuth }) {
  let adding = false;
  const showError = (message) => { dom.error.textContent = message; dom.error.hidden = false; };

  dom.form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (adding) return;
    const checked = validateHost(dom.host.value);
    if (checked.error) {
      showError(checked.error);
      dom.host.focus();
      return;
    }
    adding = true;
    dom.submit.disabled = true;
    dom.submit.textContent = "Adding…";
    try {
      const result = await control.addHost(checked.host);
      if (result.ok) {
        dom.dialog.close();
        dom.host.value = "";
        notify(`Added ${checked.host}.`);
      } else {
        showError(result.error);
        if (!control.allowed) notify(result.error, true);
      }
      onChange();
    } finally {
      adding = false;
      dom.submit.disabled = false;
      dom.submit.textContent = "Add";
    }
  });

  return {
    open() {
      if (!control.allowed) return requestAuth();
      dom.error.hidden = true;
      dom.dialog.showModal();
    },
    close: () => dom.dialog.close(),
  };
}
