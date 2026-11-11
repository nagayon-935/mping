import { test } from "node:test";
import assert from "node:assert/strict";
import { clippedPoints, finiteValues, percentile, robustAxis, rttAxis, ROBUST_MIN_MS } from "../static/js/scale.js";

test("rttAxis rounds the tick interval to a readable value and avoids decimal artefacts", () => {
  for (const peak of [0, 0.3, 1.2, 18, 95]) {
    const { max, ticks } = rttAxis(peak);
    assert.equal(ticks.length, 5);
    assert.equal(ticks[0], 0);
    assert.equal(ticks[4], max);
    assert.ok(max >= peak, `${max} < ${peak}`);
    for (const v of ticks) assert.ok(!/\.\d{4,}/.test(String(v)), `imprecise tick: ${v}`);
  }
});

test("percentile uses the nearest rank and ignores lost and missing samples", () => {
  const series = [undefined, null, ...Array.from({ length: 100 }, (_, i) => i + 1)];
  assert.equal(percentile(series, 0.95), 95);
  assert.equal(percentile(series, 1), 100);
  assert.equal(percentile(series, 0), 1);
  assert.equal(percentile([null, undefined], 0.95), 0);
  assert.deepEqual(finiteValues([1, null, undefined, NaN, Infinity, 2]), [1, 2]);
});

test("a single spike does not raise the robust top; it is clipped instead", () => {
  const series = Array.from({ length: 300 }, (_, i) => 12 + (i % 5) * 8); // 12..44 ms
  series[200] = 680;
  const axis = robustAxis(series);
  assert.equal(axis.p95, 44);
  assert.ok(axis.max > axis.p95, "top sits above the p95");
  assert.ok(axis.max < 100, `top stays near the bulk of samples, got ${axis.max}`);
  assert.equal(axis.ticks.at(-1), axis.max);
  assert.equal(axis.peak, 680);
  assert.equal(axis.clipped, true);
});

test("a steady series is not clipped", () => {
  const axis = robustAxis([10, 11, 12, 13, null, 12]);
  assert.equal(axis.clipped, false);
  assert.ok(axis.max >= 13);
});

test("the robust top has a minimum so sub-millisecond noise stays readable", () => {
  const axis = robustAxis([0.05, 0.06, 0.07]);
  assert.ok(axis.max >= ROBUST_MIN_MS);
  assert.equal(axis.clipped, false);
  assert.ok(robustAxis([]).max > 0, "an empty window still has an axis");
  assert.ok(robustAxis([0.2], { minimum: 50 }).max >= 50);
});

test("clippedPoints lists every sample above the top and the highest one", () => {
  const { indexes, peak } = clippedPoints([5, 120, null, undefined, 680, 40], 100);
  assert.deepEqual(indexes, [1, 4]);
  assert.deepEqual(peak, { index: 4, value: 680 });
  assert.deepEqual(clippedPoints([1, 2, null], 100), { indexes: [], peak: null });
});
