import { test } from "node:test";
import assert from "node:assert/strict";
import { portLatencyCell } from "../static/js/checks.js";

test("port latency only measures open checks and marks elapsed timeouts", (t) => {
  globalThis.document = {
    createElement() {
      const cell = { setAttribute() {} };
      cell.classList = { add(name) { cell.className += ` ${name}`; } };
      return cell;
    },
  };
  t.after(() => delete globalThis.document);
  for (const [status, rtt_ms, text, muted] of [
    ["Open", 5, "5.00 ms", false],
    ["Filtered", 1000, "timeout", true],
    ["Open|Filtered", 1000, "timeout", true],
    ["Closed", 1, "–", false],
    ["Error", 0, "–", false],
    ["Checking...", 0, "–", false],
  ]) {
    const cell = portLatencyCell({ status, rtt_ms });
    assert.equal(cell.textContent, text);
    assert.equal(cell.className.includes("muted"), muted);
  }
});
