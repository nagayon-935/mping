import { test } from "node:test";
import assert from "node:assert/strict";
import { rttAxis } from "../static/js/chart.js";
import { sharedMax } from "../static/js/timeline.js";

test("RTT axes round the interval, avoiding awkward quarter ticks", () => {
  assert.deepEqual(rttAxis(30).ticks, [0, 10, 20, 30, 40]);
  assert.deepEqual(rttAxis(80).ticks, [0, 50, 100, 150, 200]);
  for (const peak of [0, .01, .8, 4, 17, 90, 850, 12345]) {
    const { max, ticks } = rttAxis(peak);
    assert.ok(max >= peak * 1.15 && max > 0);
    assert.equal(ticks[0], 0);
    assert.equal(ticks.at(-1), max);
    ticks.slice(1).forEach((v, i) => assert.ok(Math.abs(v - ticks[i] - max / 4) < 1e-9));
  }
});

test("shared RTT axes cover slow, fast and empty targets with identical ticks", () => {
  const series = [[1, 2, null], [20, 30, 40], [], [undefined, null]];
  const peak = sharedMax(series);
  const axes = series.map(() => rttAxis(peak));
  for (const axis of axes) assert.deepEqual(axis.ticks, [0, 20, 40, 60, 80]);
  assert.ok(rttAxis(sharedMax([[], [null]])).max > 0);
});
