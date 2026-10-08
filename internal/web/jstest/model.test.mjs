// Unit tests for the dashboard's pure logic. Run with:
//   node --test "internal/web/jstest/*.test.mjs"
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  levelAbove,
  lossRate,
  rowLevel,
  statusReasons,
  cellLevels,
  hopLossLevel,
  formatMs,
  formatRTT,
  formatCount,
  selectColumns,
  tableScale,
  formatPct,
  buildSections,
  summarize,
  matchesFilter,
  badgeFor,
  dscpName,
  tokenFromHash,
  validateHost,
  summaryItems,
  readOnlyHint,
} from "../static/js/model.js";

const th = {
  rtt_warn_ms: 50, rtt_crit_ms: 200,
  jitter_warn_ms: 10, jitter_crit_ms: 50,
  loss_warn_pct: 20, loss_crit_pct: 80,
};

const target = (over = {}) => ({
  id: 1, host: "a.example", ip: "192.0.2.1", sent: 10, recv: 10, loss: 0,
  last_rtt_ms: 12, avg_rtt_ms: 12, min_rtt_ms: 10, max_rtt_ms: 15, jitter_ms: 1,
  ...over,
});

test("levelAbove is strictly greater-than, like the TUI", () => {
  assert.equal(levelAbove(50, 50, 200), "ok");
  assert.equal(levelAbove(50.1, 50, 200), "warn");
  assert.equal(levelAbove(200, 50, 200), "warn");
  assert.equal(levelAbove(200.1, 50, 200), "crit");
});

test("levelAbove treats missing or zero samples as none", () => {
  assert.equal(levelAbove(0, 50, 200), "none");
  assert.equal(levelAbove(undefined, 50, 200), "none");
  assert.equal(levelAbove(null, 50, 200), "none");
});

test("lossRate excludes in-flight probes (loss / (recv + loss))", () => {
  assert.equal(lossRate(target({ sent: 11, recv: 6, loss: 4 })), 40);
  assert.equal(lossRate(target({ sent: 1, recv: 0, loss: 0 })), 0);
});

test("rowLevel is pending before any probe completes", () => {
  assert.equal(rowLevel(target({ sent: 1, recv: 0, loss: 0 }), th), "pending");
});

test("rowLevel takes the worst of loss, last RTT and jitter", () => {
  assert.equal(rowLevel(target(), th), "ok");
  assert.equal(rowLevel(target({ last_rtt_ms: 60 }), th), "warn");
  assert.equal(rowLevel(target({ jitter_ms: 60 }), th), "crit");
  assert.equal(rowLevel(target({ recv: 1, loss: 9 }), th), "crit");
});

test("rowLevel is crit when every probe was lost", () => {
  assert.equal(rowLevel(target({ recv: 0, loss: 5, last_rtt_ms: 0, jitter_ms: 0 }), th), "crit");
});

test("status reasons distinguish missing replies, current threshold breaches and historical peaks", () => {
  assert.deepEqual(statusReasons(target({ recv: 0, loss: 0 }), th), ["Waiting for a completed probe"]);
  assert.deepEqual(statusReasons(target({ recv: 0, loss: 5 }), th), ["No replies received"]);
  assert.deepEqual(statusReasons(target({ max_rtt_ms: 900, avg_rtt_ms: 300 }), th), ["Within thresholds"]);
  assert.deepEqual(statusReasons(target({ recv: 6, loss: 4, last_rtt_ms: 300, jitter_ms: 11 }), th),
    ["Loss 40.0% > 20%", "RTT 300 ms > 200 ms", "Jitter 11.0 ms > 10 ms"]);
  assert.deepEqual(statusReasons(target({ last_rtt_ms: 50, jitter_ms: 10 }), th), ["Within thresholds"]);
  assert.ok(statusReasons(target(), { ...th, rtt_warn_ms: 0 })[0].endsWith("> 0 ms"));
});

test("cellLevels colours each RTT column by its own value", () => {
  const got = cellLevels(target({ last_rtt_ms: 300, avg_rtt_ms: 60, min_rtt_ms: 0, jitter_ms: 11 }), th);
  assert.equal(got.last, "crit");
  assert.equal(got.avg, "warn");
  assert.equal(got.min, "none");
  assert.equal(got.jitter, "warn");
  assert.equal(got.loss, "ok");
});

test("hopLossLevel mirrors the MTR pane (>= crit, > 0 warn)", () => {
  assert.equal(hopLossLevel(0, th), "ok");
  assert.equal(hopLossLevel(0.1, th), "warn");
  assert.equal(hopLossLevel(80, th), "crit");
});

test("formatMs picks precision by magnitude and dashes missing values", () => {
  assert.equal(formatMs(0), "–");
  assert.equal(formatMs(undefined), "–");
  assert.equal(formatMs(0.4123), "0.412");
  assert.equal(formatMs(5.678), "5.68");
  assert.equal(formatMs(56.78), "56.8");
  assert.equal(formatMs(567.8), "568");
});

test("formatPct shows one decimal", () => {
  assert.equal(formatPct(0), "0.0");
  assert.equal(formatPct(33.333), "33.3");
});

test("RTT and counter display budgets hold at rounding and int64/duration limits", () => {
  for (const ms of [0.0001, 0.9999, 9.9999, 99.9999, 999999.9, 1e6, 9223372036854.775]) {
    assert.ok(formatRTT(ms).length <= 10, `${ms}: ${formatRTT(ms)}`);
    assert.match(formatRTT(ms), / ms$/);
  }
  assert.equal(formatRTT(0), "–");
  assert.equal(formatRTT(1000000), "1.00e6 ms");
  assert.equal(formatRTT(1000000, false), "1000000 ms");
  for (const n of [0, 999999, 1000000, 999999999, 1000000000, 999999999999999, 1e15, 9223372036854775807]) {
    assert.ok(formatCount(n).length <= 8, `${n}: ${formatCount(n)}`);
  }
  assert.equal(formatCount(999999), "999,999");
  assert.equal(formatCount(1500000), "1.5M");
});

test("formatCount carries to the next unit when rounding reaches 1000", () => {
  const cases = [
    [999949999, "999.9M"], [999950000, "1B"], [999999999, "1B"],
    [999949999999, "999.9B"], [999950000000, "1T"], [999999999999, "1T"],
    [999949999999999, "999.9T"], [999950000000000, "1.00e15"],
    [999999999999999, "1.00e15"], [1e15, "1.00e15"],
  ];
  for (const [n, want] of cases) assert.equal(formatCount(n), want, String(n));
});

test("buildSections without groups is a single unnamed section", () => {
  const ts = [target({ id: 1 }), target({ id: 2 })];
  assert.deepEqual(buildSections(ts, []).map((s) => [s.name, s.targets.map((t) => t.id)]), [[null, [1, 2]]]);
});

test("buildSections follows groups, then collects the rest as Ungrouped", () => {
  const ts = [target({ id: 1 }), target({ id: 2 }), target({ id: 3 })];
  const groups = [{ name: "core", target_ids: [3, 1, 99] }];
  assert.deepEqual(
    buildSections(ts, groups).map((s) => [s.name, s.targets.map((t) => t.id)]),
    [["core", [3, 1]], ["Ungrouped", [2]]],
  );
});

test("buildSections drops groups left empty", () => {
  const ts = [target({ id: 1 })];
  const groups = [{ name: "gone", target_ids: [42] }, { name: "core", target_ids: [1] }];
  assert.deepEqual(buildSections(ts, groups).map((s) => s.name), ["core"]);
});

test("summarize counts targets per level", () => {
  const ts = [target(), target({ last_rtt_ms: 60 }), target({ recv: 0, loss: 3 }), target({ sent: 1, recv: 0 })];
  assert.deepEqual(summarize(ts, th), { total: 4, ok: 1, warn: 1, crit: 1, pending: 1 });
});

test("matchesFilter searches host, IP, PTR and AS fields case-insensitively", () => {
  const t = target({ host: "Core-RTR", ptr: "rtr1.example.net", asn: "AS13335", org: "Cloudflare" });
  assert.ok(matchesFilter(t, ""));
  assert.ok(matchesFilter(t, "core"));
  assert.ok(matchesFilter(t, "192.0.2"));
  assert.ok(matchesFilter(t, "RTR1"));
  assert.ok(matchesFilter(t, "cloudflare"));
  assert.ok(!matchesFilter(t, "google"));
});

test("badgeFor keeps a final stopped state when the stream drops", () => {
  assert.equal(badgeFor("running", true).label, "Live");
  assert.equal(badgeFor("reloading", true).label, "Reloading");
  assert.equal(badgeFor("running", false).label, "Disconnected");
  assert.equal(badgeFor("stopped", false).label, "Stopped");
  assert.equal(badgeFor(null, false).label, "Connecting");
});

test("dscpName mirrors pinger.DSCPName: top 6 bits, ECN ignored, decimal fallback", () => {
  assert.equal(dscpName(0), "–");
  assert.equal(dscpName(184), "EF");
  assert.equal(dscpName(185), "EF");
  assert.equal(dscpName(32), "CS1");
  assert.equal(dscpName(4), "1");
});

test("tokenFromHash reads only a #token= fragment", () => {
  assert.equal(tokenFromHash("#token=abc123"), "abc123");
  assert.equal(tokenFromHash("#target-4"), null);
  assert.equal(tokenFromHash(""), null);
  assert.equal(tokenFromHash("#token="), null);
});

test("validateHost mirrors the server's shape checks", () => {
  assert.deepEqual(validateHost("  a.example "), { host: "a.example" });
  assert.deepEqual(validateHost("2001:db8::1"), { host: "2001:db8::1" });
  assert.ok(validateHost("   ").error);
  assert.ok(validateHost("a b").error);
  assert.ok(validateHost("a\u0007b").error);
  assert.ok(validateHost("a".repeat(254)).error);
  assert.equal(validateHost("a".repeat(253)).host.length, 253);
});

test("summaryItems lists waiting targets only when there are some", () => {
  assert.deepEqual(summaryItems({ total: 3, ok: 1, warn: 1, crit: 1, pending: 0 }).map(([, l]) => l), ["targets", "OK", "warn", "crit"]);
  assert.deepEqual(summaryItems({ total: 2, ok: 1, warn: 0, crit: 0, pending: 1 }), [[2, "targets"], [1, "OK"], [0, "warn"], [0, "crit"], [1, "waiting"]]);
});

test("readOnlyHint explains the 127.0.0.1 origin when opened via another host name", () => {
  assert.match(readOnlyHint("127.0.0.1").title, /Log pane/);
  assert.doesNotMatch(readOnlyHint("127.0.0.1").title, /instead/);
  assert.match(readOnlyHint("localhost").title, /127\.0\.0\.1/);
  assert.match(readOnlyHint("[::1]").text, /Read-only/);
});

const col = (key, priority, required = false) => ({ key, priority, required });
const w = { status: 100, host: 100, loss: 50, last: 80, avg: 80, jitter: 80, spark: 120, ip: 150, min: 80 };
const widthOf = (c) => w[c.key];
// Display order differs from priority order on purpose.
const cols = [col("status", 0, true), col("host", 0, true), col("ip", 6), col("loss", 1),
  col("last", 2), col("avg", 3), col("min", 7), col("jitter", 4), col("spark", 5)];
const keys = (list) => list.map((c) => c.key);

test("selectColumns shows every column when there is room", () => {
  assert.deepEqual(keys(selectColumns(cols, widthOf, 10000)), keys(cols));
});

test("selectColumns drops the least important columns first and keeps display order", () => {
  // Required 200 + loss 50 + last 80 + avg 80 + jitter 80 + spark 120 = 610.
  assert.deepEqual(keys(selectColumns(cols, widthOf, 610)), ["status", "host", "loss", "last", "avg", "jitter", "spark"]);
  assert.deepEqual(keys(selectColumns(cols, widthOf, 609)), ["status", "host", "loss", "last", "avg", "jitter"]);
  assert.deepEqual(keys(selectColumns(cols, widthOf, 330)), ["status", "host", "loss", "last"]);
});

test("selectColumns never skips ahead of a column that did not fit", () => {
  // spark (120) no longer fits at 520, but ip (150) is wider and min (80) is narrower than
  // spark; neither may take its place once a higher-priority column was dropped.
  assert.deepEqual(keys(selectColumns(cols, widthOf, 520)), ["status", "host", "loss", "last", "avg", "jitter"]);
});

test("selectColumns always keeps required columns, even when they overflow", () => {
  assert.deepEqual(keys(selectColumns(cols, widthOf, 10)), ["status", "host"]);
});

test("selectColumns ignores columns the caller left out", () => {
  const some = cols.filter((c) => c.key !== "avg");
  assert.deepEqual(keys(selectColumns(some, widthOf, 10000)), keys(some));
});

test("tableScale never shrinks the table below its natural size", () => {
  assert.equal(tableScale(1000, 900, 1.6), 1);
  assert.equal(tableScale(1000, 1000, 1.6), 1);
});

test("tableScale grows the table to fill the available width", () => {
  assert.equal(tableScale(1000, 1250, 1.6), 1.25);
  assert.equal(tableScale(1772, 1843, 1.6), 1.04);
});

test("tableScale stops at the cap, leaving the remainder empty", () => {
  assert.equal(tableScale(1000, 5000, 1.6), 1.6);
});

test("tableScale rounds down so the scaled table cannot overflow by a pixel", () => {
  const scale = tableScale(1333, 2000, 2);
  assert.ok(1333 * scale <= 2000, String(scale));
  assert.ok(2000 - 1333 * scale < 2, String(scale));
});

test("tableScale ignores unusable widths", () => {
  assert.equal(tableScale(0, 1000, 1.6), 1);
  assert.equal(tableScale(1000, 0, 1.6), 1);
  assert.equal(tableScale(NaN, 1000, 1.6), 1);
});
