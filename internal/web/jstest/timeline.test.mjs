// Unit tests for the RTT graphs pane's time-window logic. Run with:
//   node --test "internal/web/jstest/*.test.mjs"
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  windowOptions,
  defaultWindow,
  sharedMax,
  agoLabel,
  indexForAgo,
  tail,
} from "../static/js/timeline.js";

const labels = (opts) => opts.map((o) => o.label);

test("windowOptions offers the windows the history ring can cover", () => {
  // 1s interval, 3000-sample ring: up to 50 minutes.
  assert.deepEqual(labels(windowOptions(1000, 3000)), ["1m", "5m", "15m", "30m"]);
  // 100ms interval: 300s of history.
  assert.deepEqual(labels(windowOptions(100, 3000)), ["1m", "5m"]);
  // 10s interval: everything fits.
  assert.deepEqual(labels(windowOptions(10000, 3000)), ["1m", "5m", "15m", "30m", "1h"]);
});

test("windowOptions converts a window to a sample count", () => {
  const five = windowOptions(1000, 3000).find((o) => o.label === "5m");
  assert.equal(five.points, 300);
  assert.equal(five.seconds, 300);
  const oneAt250 = windowOptions(250, 3000).find((o) => o.label === "1m");
  assert.equal(oneAt250.points, 240);
});

test("windowOptions falls back to the whole ring when even 1m does not fit", () => {
  // 10ms interval, 3000 samples = 30s.
  const opts = windowOptions(10, 3000);
  assert.equal(opts.length, 1);
  assert.equal(opts[0].points, 3000);
  assert.equal(opts[0].label, "30s");
});

test("defaultWindow prefers 5m, else the widest available", () => {
  assert.equal(defaultWindow(windowOptions(1000, 3000)).label, "5m");
  assert.equal(defaultWindow(windowOptions(10, 3000)).label, "30s");
  assert.equal(defaultWindow([{ label: "1m", seconds: 60, points: 60 }]).label, "1m");
});

test("sharedMax is the largest sample across every series, ignoring losses", () => {
  assert.equal(sharedMax([[1, null, 3], [null, 7.5], []]), 7.5);
  assert.equal(sharedMax([[null], []]), 0);
  assert.equal(sharedMax([]), 0);
});

test("agoLabel reads samples-from-the-end as elapsed time", () => {
  assert.equal(agoLabel(0, 1000), "now");
  assert.equal(agoLabel(12, 1000), "12s ago");
  assert.equal(agoLabel(80, 1000), "1m 20s ago");
  assert.equal(agoLabel(120, 1000), "2m ago");
  assert.equal(agoLabel(3, 250), "1s ago");
  assert.equal(agoLabel(1, 100), "<1s ago");
});

test("indexForAgo aligns series of different lengths at their newest sample", () => {
  assert.equal(indexForAgo(0, 10), 9);
  assert.equal(indexForAgo(4, 10), 5);
  // A host added later has a shorter series: older cursors fall before it.
  assert.equal(indexForAgo(12, 10), -1);
  assert.equal(indexForAgo(-1, 10), -1);
});

test("tail returns the newest n samples without copying more than needed", () => {
  assert.deepEqual(tail([1, 2, 3, 4], 2), [3, 4]);
  assert.deepEqual(tail([1, 2], 5), [1, 2]);
  assert.deepEqual(tail(undefined, 3), []);
});
