import { $ } from "./dom.js";

/** Keep the current page, inspected host, and browser hash in sync. */
export function createNavigation(view, scheduleRender, clearInspect) {
  const hasTarget = (id) => !!view.snapshot?.targets.some((t) => t.id === id);
  const monitorHash = () => (view.selectedId == null ? "#monitor" : `#target-${view.selectedId}`);
  const targetFromHash = () => {
    const m = /^#target-(\d+)$/.exec(location.hash);
    return m ? Number(m[1]) : null;
  };

  /** Rows toggle: selecting the selected host again returns to Activity. */
  function toggleTarget(id) {
    if (view.selectedId === id && view.inspectOpen) {
      clearSelection();
      focusRow(id);
    } else {
      selectTarget(id);
    }
  }

  /** Keep keyboard users on the host they just closed. */
  function focusRow(id) {
    requestAnimationFrame(() => $("targets").querySelector(`.host-open[data-target-id="${id}"]`)?.focus({ preventScroll: true }));
  }

  function selectTarget(id) {
    if (!hasTarget(id)) return;
    view.selectedId = id;
    view.inspectOpen = true;
    view.page = "monitor";
    history.replaceState(null, "", `#target-${id}`);
    scheduleRender();
  }

  function clearSelection() {
    view.selectedId = null;
    view.inspectOpen = false;
    clearInspect();
    if (location.hash) history.replaceState(null, "", location.pathname);
    scheduleRender();
  }

  function showMonitorPage() {
    view.page = "monitor";
    history.replaceState(null, "", monitorHash());
    scheduleRender();
  }

  function readPage() {
    view.page = location.hash === "#log" ? "log" : location.hash === "#http" ? "http" : "monitor";
    const wanted = targetFromHash();
    if (wanted != null && view.snapshot) selectTarget(wanted);
    scheduleRender();
  }

  function restoreSelection() {
    // Restore a selection from the address (reload or a shared link); a link
    // to a target that no longer exists is dropped rather than kept forever.
    const wanted = targetFromHash();
    if (wanted != null && view.selectedId == null) {
      if (hasTarget(wanted)) selectTarget(wanted);
      else history.replaceState(null, "", location.pathname);
    }
  }

  function renderPages(log, activity) {
    if (view.page === "http" && view.meta && !view.meta.features.http) {
      view.page = "monitor";
      history.replaceState(null, "", "#monitor");
    }
    for (const page of ["monitor", "log", "http"]) {
      $(page === "monitor" ? "workspace" : `page-${page}`).hidden = view.page !== page;
      const link = $(`nav-${page}`);
      if (view.page === page) link.setAttribute("aria-current", "page");
      else link.removeAttribute("aria-current");
    }
    $("nav-monitor").href = monitorHash();
    $("nav-http").hidden = !view.meta?.features.http;
    log.activate(view.page === "log");
    if (view.page !== "monitor") activity.activate(false);
  }

  return {
    hasTarget, renderPages, toggleTarget, focusRow, selectTarget, clearSelection,
    showMonitorPage, readPage, restoreSelection,
  };
}
