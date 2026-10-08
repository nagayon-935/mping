// Unit tests for the RTT graphs pane's time-window logic. Run with:
//   node --test "internal/web/jstest/*.test.mjs"
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  windowOptions,
  defaultWindow,
  sharedMax,
  agoLabel,
  padTo,
  tail,
  downsample,
  reconcileWindow,
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

test("padTo right-aligns every series to the window length", () => {
  // A host added later is padded with undefined ("no data"), never null
  // (null means a lost probe and is drawn as a red tick).
  assert.deepEqual(padTo([5, null, 7], 5), [undefined, undefined, 5, null, 7]);
  assert.deepEqual(padTo([1, 2, 3, 4], 2), [3, 4]);
  assert.deepEqual(padTo([1, 2], 2), [1, 2]);
  assert.deepEqual(padTo(undefined, 2), [undefined, undefined]);
});

test("tail returns the newest n samples without copying more than needed", () => {
  assert.deepEqual(tail([1, 2, 3, 4], 2), [3, 4]);
  assert.deepEqual(tail([1, 2], 5), [1, 2]);
  assert.deepEqual(tail(undefined, 3), []);
});

test("downsample keeps short series as they are", () => {
  const got = downsample([1, null, 3], 10);
  assert.deepEqual(got.values, [1, null, 3]);
  assert.deepEqual(got.lost, [false, true, false]);
  assert.equal(got.size, 1);
});

test("downsample buckets from the newest sample, keeping each bucket's peak", () => {
  // 7 samples into at most 3 buckets of 3: the oldest bucket is the partial one.
  const got = downsample([1, 2, 9, 3, 4, 5, 6], 3);
  assert.equal(got.size, 3);
  assert.deepEqual(got.values, [1, 9, 6]);
  assert.deepEqual(got.lost, [false, false, false]);
});

test("downsample keeps a lost probe visible even when the bucket also has replies", () => {
  const got = downsample([5, null, 7, 8], 2);
  assert.deepEqual(got.values, [7, 8]);
  assert.deepEqual(got.lost, [true, false]);
});

test("downsample marks an all-lost bucket as null and an empty one as undefined", () => {
  const got = downsample([undefined, undefined, null, null], 2);
  assert.deepEqual(got.values, [undefined, null]);
  assert.deepEqual(got.lost, [false, true]);
});

test("reconcileWindow keeps the chosen window when the new interval still offers it", () => {
  const got = reconcileWindow("5m", 2000, 3000);
  assert.equal(got.window.label, "5m");
  assert.equal(got.window.points, 150);
  assert.deepEqual(got.options.map((o) => o.label), ["1m", "5m", "15m", "30m", "1h"]);
});

test("reconcileWindow falls back to the default when the window no longer fits", () => {
  // 100ms interval: only 1m and 5m fit; 30m must fall back to 5m.
  const got = reconcileWindow("30m", 100, 3000);
  assert.equal(got.window.label, "5m");
  assert.equal(got.window.points, 3000);
});
