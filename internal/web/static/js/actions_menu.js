// Header controls. On narrow screens they collapse behind an "Actions"
// toggle; CSS decides whether the toggle is shown, this module only tracks
// whether the collapsed menu is open.

/**
 * @param {{toggle: HTMLButtonElement, menu: HTMLElement}} dom
 */
export function createActionsMenu({ toggle, menu }) {
  const isOpen = () => toggle.getAttribute("aria-expanded") === "true";
  const setOpen = (open) => {
    toggle.setAttribute("aria-expanded", String(open));
    menu.classList.toggle("actions-open", open);
  };

  toggle.addEventListener("click", () => setOpen(!isOpen()));
  // Any action closes the menu; so does a click anywhere else.
  menu.addEventListener("click", (event) => {
    if (event.target.closest("button")) setOpen(false);
  });
  document.addEventListener("click", (event) => {
    if (!menu.contains(event.target) && !toggle.contains(event.target)) setOpen(false);
  });

  // A menu action is hidden once it opens a dialog. When that dialog closes,
  // return focus to the toggle after the browser finishes its own restore.
  for (const dialog of document.querySelectorAll("dialog")) {
    dialog.addEventListener("close", () => requestAnimationFrame(() => {
      const active = document.activeElement;
      if (toggle.getBoundingClientRect().width &&
          (active === document.body || dialog.contains(active) || !active.getClientRects().length)) {
        toggle.focus();
      }
    }));
  }

  return {
    isOpen,
    /** Closes the menu, optionally moving focus back to its toggle. */
    close({ focus = false } = {}) {
      setOpen(false);
      if (focus) toggle.focus();
    },
    /** Controls exist only while this page may make changes. */
    setAvailable(on) {
      menu.hidden = !on;
      toggle.hidden = !on;
    },
  };
}
