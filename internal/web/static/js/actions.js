// Operations that change mping's state from the browser: deleting a host,
// stopping/restarting all measurements and resetting statistics. Destructive
// ones go through the confirmation dialog.

/**
 * @param {{
 *   control: object, confirmation: object, button: HTMLButtonElement,
 *   notify: (message: string, isError?: boolean) => void,
 *   findTarget: (id: number) => object | undefined,
 *   isStopped: () => boolean,
 *   onDeleted: (target: object) => void,
 *   onReset: () => Promise<void>,
 *   onMeasurementChange: (ok: boolean) => Promise<void>,
 *   afterChange: () => void,
 * }} deps
 */
export function createActions(deps) {
  const { control, confirmation, notify } = deps;

  return {
    deleteTarget(id) {
      if (!control.allowed) return;
      const target = deps.findTarget(id);
      if (!target) return;
      confirmation.open({
        title: "Delete host?",
        label: "Delete host",
        description: `${target.host}\n${target.ip || "Address not resolved"}\nTarget #${target.id}\n\n` +
          "Measurements for this host will stop. Re-adding it starts a new measurement.",
        async run() {
          const result = await control.deleteTarget(id);
          if (result.ok) {
            notify(`Deleted ${target.host}.`);
            deps.onDeleted(target);
          }
          deps.afterChange();
          return result;
        },
      });
    },

    confirmReset() {
      confirmation.open({
        title: "Reset all statistics?",
        label: "Reset statistics",
        description: "Reset counters and RTT histories for all hosts and enabled monitors.\n\n" +
          "The current running/stopped state is retained. This cannot be undone.",
        async run() {
          const result = await control.reset();
          if (result.ok) {
            notify("Statistics reset.");
            await deps.onReset();
          }
          deps.afterChange();
          return result;
        },
      });
    },

    async toggleMeasurements() {
      const restart = deps.isStopped();
      const { button } = deps;
      button.disabled = true;
      button.textContent = restart ? "Restarting…" : "Stopping…";
      try {
        const result = await (restart ? control.restart() : control.stop());
        if (result.ok) notify(restart ? "All measurements restarted." : "All measurements stopped. Statistics are retained.");
        else notify(result.error, true);
        await deps.onMeasurementChange(result.ok);
      } finally {
        button.disabled = false;
        deps.afterChange();
      }
    },
  };
}
