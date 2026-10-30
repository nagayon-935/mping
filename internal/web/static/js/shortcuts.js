// Global keyboard handling: roving focus within the host list plus the
// single-character shortcuts listed in the help dialog. Character shortcuts
// can be turned off; they never fire while typing, composing or in a dialog.
import { editableTarget } from "./workspace.js";

/**
 * @param {{
 *   table: HTMLTableElement, filter: HTMLInputElement,
 *   charactersEnabled: () => boolean, canControl: () => boolean,
 *   focusFilter: () => void, openHelp: () => void, openReport: () => void,
 *   deleteTarget: (id: number) => void,
 * }} deps
 */
export function installShortcuts(deps) {
  document.addEventListener("keydown", (event) => {
    if (event.defaultPrevented || event.isComposing || event.ctrlKey || event.metaKey || event.altKey ||
        document.querySelector("dialog[open]")) return;

    if (editableTarget(event.target)) {
      // Escape leaves search for the host list without clearing the query.
      if (event.key === "Escape" && event.target === deps.filter) {
        event.preventDefault();
        deps.table.querySelector(".host-open[tabindex='0']")?.focus();
      }
      return;
    }

    const characters = deps.charactersEnabled();
    const row = event.target.closest?.(".target-row");
    if (row && deps.table.contains(row) && handleRowKey(event, row, characters, deps)) return;
    if (!characters) return;

    if (event.key === "/") {
      event.preventDefault();
      deps.focusFilter();
    } else if (event.key === "?") {
      event.preventDefault();
      deps.openHelp();
    } else if (event.key === "w" && deps.canControl()) {
      event.preventDefault();
      deps.openReport();
    }
  });
}

/** Moves focus within the host list; returns whether the key was handled. */
function handleRowKey(event, row, characters, deps) {
  const buttons = [...deps.table.querySelectorAll(".host-open")];
  const index = buttons.findIndex((button) => button.dataset.targetId === row.dataset.targetId);
  let next;
  if (event.key === "ArrowDown" || (characters && event.key === "j")) next = Math.min(buttons.length - 1, index + 1);
  else if (event.key === "ArrowUp" || (characters && event.key === "k")) next = Math.max(0, index - 1);
  else if (event.key === "Home") next = 0;
  else if (event.key === "End") next = buttons.length - 1;

  if (next !== undefined) {
    event.preventDefault();
    buttons[next]?.focus({ preventScroll: true });
    buttons[next]?.closest("tr").scrollIntoView({ block: "nearest" });
    return true;
  }
  if (event.key === "Delete" && deps.canControl()) {
    event.preventDefault();
    deps.deleteTarget(Number(row.dataset.targetId));
    return true;
  }
  return false;
}
