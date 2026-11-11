import { test } from "node:test";
import assert from "node:assert/strict";
import { sparkGeometry, sparkLabel, SPARK_MAX_POINTS } from "../static/js/spark.js";

const box = { width: 100, height: 22, max: 40 };
const points = (run) => run.split(" ").map((p) => p.split(",").map(Number));

test("points span the box from oldest (left) to newest (right)", () => {
  const g = sparkGeometry([0, 20, 40], box);
  assert.equal(g.runs.length, 1);
  assert.deepEqual(points(g.runs[0]), [[0, 21], [50, 11], [100, 1]]);
  assert.equal(g.min, 0);
  assert.equal(g.max, 40);
  assert.equal(g.label, "RTT 0.00 to 40 ms over the last 60 seconds");
});

test("lost probes break the line and are reported by position", () => {
  const g = sparkGeometry([10, 12, null, 11, 13], box);
  assert.equal(g.runs.length, 2);
  assert.deepEqual(g.losses, [50]);
  assert.equal(g.lostCount, 1);
  assert.equal(g.allLost, false);
  assert.match(g.label, /, 1 lost$/);
});

test("a window of only lost probes is flagged so it can be drawn as a red baseline", () => {
  const g = sparkGeometry([null, null, undefined, null], box);
  assert.equal(g.allLost, true);
  assert.equal(g.empty, false);
  assert.deepEqual(g.runs, []);
  assert.equal(g.label, "No replies in the last 60 seconds");
});

test("no completed probe at all is empty, not lost", () => {
  const g = sparkGeometry(new Array(60).fill(undefined), box);
  assert.equal(g.empty, true);
  assert.equal(g.allLost, false);
  assert.equal(g.label, "No measurements in the last 60 seconds");
  assert.equal(sparkGeometry([], box).empty, true);
});

test("samples above the scale top are clipped to it and listed", () => {
  const g = sparkGeometry([10, 680, 12], box);
  assert.deepEqual(points(g.runs[0])[1], [50, 1]);
  assert.deepEqual(g.clipped, [50]);
  assert.equal(g.max, 680);
});

test("an isolated sample still draws a stub", () => {
  const g = sparkGeometry([null, 20, null], box);
  assert.equal(g.runs.length, 1);
  assert.equal(points(g.runs[0]).length, 2);
});

test("dense series are reduced to at most SPARK_MAX_POINTS points, keeping losses", () => {
  const series = Array.from({ length: 600 }, (_, i) => (i === 300 ? null : 5));
  const g = sparkGeometry(series, box);
  assert.ok(points(g.runs.join(" ")).length <= SPARK_MAX_POINTS + 2);
  assert.equal(g.losses.length, 1);
});

test("labels keep a single value when min and max round alike", () => {
  assert.equal(sparkLabel({ empty: false, allLost: false, min: 12.2, max: 12.4, lostCount: 0 }), "RTT 12 ms over the last 60 seconds");
  assert.equal(sparkLabel({ empty: false, allLost: false, min: 1.25, max: 3, lostCount: 2, seconds: 30 }), "RTT 1.3 to 3.0 ms over the last 30 seconds, 2 lost");
});
