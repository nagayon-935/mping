/** A download name, never a filesystem path. Keep the selected format's suffix. */
export function reportFilename(raw, format) {
  if (!["json", "csv"].includes(format)) return { error: "Choose JSON or CSV." };
  const name = String(raw ?? "").trim();
  if (!name) return { error: "Enter a file name." };
  if (/[/\\\x00-\x1f\x7f]/.test(name)) return { error: "Use a file name without folders or control characters." };
  const base = name.replace(/\.(json|csv)$/i, "");
  if (!base || /^\.+$/.test(base)) return { error: "Enter a file name." };
  return { filename: `${base}.${format}` };
}

/** The dialog survives snapshots, so typing and format choices stay stable. */
export function createReportDialog(dom) {
  let request = null;
  const error = (message = "") => {
    dom.error.textContent = message;
    dom.error.hidden = !message;
    dom.name.setAttribute("aria-invalid", String(!!message));
  };
  const busy = (on) => {
    dom.submit.disabled = on;
    dom.format.disabled = on;
    dom.name.disabled = on;
    dom.submit.textContent = on ? "Downloading…" : "Download";
  };
  dom.format.addEventListener("change", () => {
    const result = reportFilename(dom.name.value, dom.format.value);
    if (result.filename) dom.name.value = result.filename;
    error();
  });
  dom.name.addEventListener("input", () => error());
  const cancel = () => { request?.abort(); if (dom.dialog.open) dom.dialog.close(); };
  dom.cancel.addEventListener("click", cancel);
  dom.dialog.addEventListener("cancel", () => request?.abort());
  dom.dialog.addEventListener("close", () => request?.abort());
  dom.form.addEventListener("submit", async (event) => {
    event.preventDefault();
    if (request) return;
    const result = reportFilename(dom.name.value, dom.format.value);
    if (result.error) { error(result.error); dom.name.focus(); return; }
    dom.name.value = result.filename;
    error();
    const controller = new AbortController();
    request = controller;
    busy(true);
    try {
      const response = await dom.download(dom.format.value, result.filename, { signal: controller.signal });
      if (controller.signal.aborted) return;
      if (response.ok) dom.dialog.close();
      else {
        error(response.error);
        dom.onError(response.error);
      }
    } finally {
      request = null;
      busy(false);
    }
  });
  return {
    open() {
      if (!dom.canControl() || dom.dialog.open || request) return;
      error();
      dom.dialog.showModal();
      dom.name.select();
    },
    close: cancel,
  };
}
