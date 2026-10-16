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
