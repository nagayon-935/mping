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
