// Unit tests for the dashboard's pure logic. Run with:
//   node --test "internal/web/jstest/*.test.mjs"
import { test } from "node:test";
import assert from "node:assert/strict";

import {
  lossRate,
  rowLevel,
  statusReason,
  cellLevels,
  formatMs,
  formatRTT,
  formatCount,
  selectColumns,
  formatPct,
  buildSections,
  summarize,
  matchesFilter,
  badgeFor,
  dscpName,
  tokenFromHash,
  validateHost,
  isIPLiteral,
  targetAddress,
} from "../static/js/model.js";

const target = (over = {}) => ({
  id: 1, host: "a.example", ip: "192.0.2.1", sent: 10, recv: 10, loss: 0,
  last_rtt_ms: 12, avg_rtt_ms: 12, min_rtt_ms: 10, max_rtt_ms: 15, jitter_ms: 1,
  ...over,
});

test("lossRate excludes in-flight probes (loss / (recv + loss))", () => {
  assert.equal(lossRate(target({ sent: 11, recv: 6, loss: 4 })), 40);
  assert.equal(lossRate(target({ sent: 1, recv: 0, loss: 0 })), 0);
});

const health = (status, reason = "") => ({ health: { configured: true, status, reason } });

test("rowLevel is the server's health status", () => {
  for (const status of ["pending", "ok", "warn", "crit"]) assert.equal(rowLevel(target(health(status))), status);
});

test("rowLevel ignores lifetime counters the server did not judge by", () => {
  // A healthy lifetime average does not hide a current outage, and the
  // other way round: only the server's judgment counts.
  assert.equal(rowLevel(target({ recv: 10000, loss: 3, ...health("crit") })), "crit");
  assert.equal(rowLevel(target({ recv: 0, loss: 50, ...health("ok") })), "ok");
});

test("rowLevel is pending without a known health status", () => {
  assert.equal(rowLevel(target()), "pending");
  assert.equal(rowLevel(target(health("bogus"))), "pending");
});

test("statusReason is the server's explanation", () => {
  assert.equal(statusReason(target(health("crit", "3 consecutive failures"))), "3 consecutive failures");
  assert.equal(statusReason(target()), "Waiting for a completed probe");
});

test("cellLevels reads the server's per-measurement levels", () => {
  assert.deepEqual(cellLevels(target({ levels: { loss: "ok", last_rtt: "crit", jitter: "warn" } })),
    { loss: "ok", last: "crit", jitter: "warn" });
  assert.deepEqual(cellLevels(target()), { loss: "none", last: "none", jitter: "none" });
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
  const ts = [target(health("ok")), target(health("warn")), target(health("crit")), target()];
  assert.deepEqual(summarize(ts), { total: 4, ok: 1, warn: 1, crit: 1, pending: 1 });
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

test("disabled optional data does not match an invisible AS or PTR field", () => {
  const t={host:"router",ip:"192.0.2.1",asn:"AS64500",org:"Example Networks",ptr:"edge.example"};
  assert.equal(matchesFilter(t,"AS64500",{asn:false,ptr:false}),false);
  assert.equal(matchesFilter(t,"edge.example",{asn:false,ptr:false}),false);
  assert.equal(matchesFilter(t,"AS64500",{asn:true,ptr:false}),true);
  assert.equal(matchesFilter(t,"edge.example",{asn:false,ptr:true}),true);
});

test("badgeFor keeps a final stopped state when the stream drops", () => {
  assert.equal(badgeFor("running", true).label, "Online");
  assert.equal(badgeFor("reloading", true).label, "Reloading");
  assert.equal(badgeFor("running", false).label, "Reconnecting");
  assert.equal(badgeFor("stopped", false).label, "Session ended");
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

test("targetAddress omits redundant IP when target is already an IP or pinned label", () => {
  assert.equal(isIPLiteral("1.1.1.1"), true);
  assert.equal(isIPLiteral("2001:4860:4860::8888"), true);
  assert.equal(isIPLiteral("fe80::1%en0"), true);
  assert.equal(isIPLiteral("one.one.one.one"), false);

  assert.equal(targetAddress({ host: "1.1.1.1", ip: "1.1.1.1" }), null);
  assert.equal(targetAddress({ host: "1.1.1.1", ip: "" }), null);
  assert.equal(targetAddress({ host: "2001:DB8::1", ip: "2001:db8::1" }), null);
  assert.equal(targetAddress({ host: "dns.google (8.8.8.8)", ip: "8.8.8.8" }), null);
  assert.equal(targetAddress({ host: "one.one.one.one", ip: "1.1.1.1" }), "1.1.1.1");
  assert.equal(targetAddress({ host: "one.one.one.one", ip: "" }), "Resolving address…");
});

