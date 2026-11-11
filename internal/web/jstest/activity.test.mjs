import { test } from "node:test";
import assert from "node:assert/strict";
import {
  activityItems, formatDuration, healthTransition, lastHourSummary, reasonWithThreshold, recentAcrossHosts,
  PER_TARGET_EVENT_CAP,
} from "../static/js/activity_model.js";
import { chooseLayout } from "../static/js/workspace.js";

const NOW = Date.parse("2026-11-11T13:20:00Z");
const ago = (s) => new Date(NOW - s * 1000).toISOString();
const host = (name, status, sinceS, reason = "Within thresholds") =>
  ({ host: name, health: { status, reason, since: ago(sinceS) } });

test("formatDuration is short and readable at every scale", () => {
  assert.equal(formatDuration(42_000), "42s");
  assert.equal(formatDuration(192_000), "3m 12s");
  assert.equal(formatDuration(180_000), "3m");
  assert.equal(formatDuration(18 * 60_000 + 5_000), "18m");
  assert.equal(formatDuration(65 * 60_000), "1h 5m");
  assert.equal(formatDuration(2 * 24 * 3600_000 + 3 * 3600_000), "2d 3h");
  assert.equal(formatDuration(null), "–");
  assert.equal(formatDuration(-1), "–");
});

test("reasons keep the value and add the threshold", () => {
  assert.equal(reasonWithThreshold("Jitter 12.3ms > 10.0ms"), "Jitter 12.3 ms (threshold 10.0 ms)");
  assert.equal(reasonWithThreshold("Recent loss 30.9% > 5.0%"), "Loss 30.9% (threshold 5.0%)");
  assert.equal(reasonWithThreshold("55 consecutive failures"), "55 consecutive failures");
});

test("few hosts: everyone is listed, problems first, then longest-lasting", () => {
  const targets = [host("ok-old", "ok", 1000), host("warn-new", "warn", 20, "Jitter 11.0ms > 10.0ms"),
    host("crit", "crit", 55, "55 consecutive failures"), host("warn-old", "warn", 700, "RTT 61.7ms > 50.0ms")];
  const { items, okCount } = activityItems(targets, { mode: "all", now: NOW });
  assert.deepEqual(items.map((i) => i.target.host), ["crit", "warn-old", "warn-new", "ok-old"]);
  assert.equal(items[0].durationMs, 55_000);
  assert.equal(items[1].reason, "RTT 61.7 ms (threshold 50.0 ms)");
  assert.equal(items[3].reason, "", "OK needs no reason");
  assert.equal(okCount, 1);
});

test("many hosts: only non-OK hosts, longest-lasting first, OK hosts counted", () => {
  const targets = [host("a", "ok", 10), host("down", "crit", 55, "55 consecutive failures"),
    host("flaky", "warn", 720, "Recent loss 30.9% > 5.0%"), host("b", "ok", 10), host("wait", "pending", 3)];
  const { items, okCount } = activityItems(targets, { mode: "attention", now: NOW });
  assert.deepEqual(items.map((i) => i.target.host), ["flaky", "down", "wait"]);
  assert.equal(okCount, 2);
});

test("a host without a since time sorts after timed ones and shows no duration", () => {
  const { items } = activityItems([{ host: "x", health: { status: "warn", reason: "r" } }, host("y", "warn", 5)], { mode: "attention", now: NOW });
  assert.deepEqual(items.map((i) => [i.target.host, i.durationMs]), [["y", 5000], ["x", null]]);
});

const ev = (h, s, kind, message) => ({ host: h, at: ago(s), kind, message, count: 1 });
const health = (h, s, msg) => ev(h, s, "health", msg);

test("healthTransition reads only health events", () => {
  assert.deepEqual(healthTransition(health("a", 1, "crit: 3 consecutive failures")), { level: "crit", reason: "3 consecutive failures" });
  assert.equal(healthTransition(ev("a", 1, "ping", "Request timeout")), null);
});

test("last hour: counts transitions by state entered and finds the least stable host", () => {
  const events = [
    health("flaky", 100, "warn: Recent loss 20.0% > 5.0%"), health("flaky", 90, "ok: Within thresholds"),
    health("flaky", 80, "warn: Recent loss 25.0% > 5.0%"), health("cdn", 60, "crit: RTT 250.0ms > 200.0ms"),
    health("old", 4000, "warn: RTT 60.0ms > 50.0ms"), ev("flaky", 50, "ping", "Request timeout"),
  ];
  const s = lastHourSummary(events, { now: NOW, limit: 1000 });
  assert.deepEqual(s.changes, { total: 4, warn: 2, crit: 1, ok: 1, pending: 0 });
  assert.deepEqual(s.leastStable, { host: "flaky", count: 3 });
  assert.deepEqual(s.simultaneous, []);
  assert.equal(s.partial, false);
});

test("last hour: one transition per host is not 'least stable'", () => {
  const s = lastHourSummary([health("a", 10, "warn: RTT 60.0ms > 50.0ms"), health("b", 10, "ok: Within thresholds")], { now: NOW });
  assert.equal(s.leastStable, null);
});

test("last hour: at least 3 hosts breaching one metric within 5 s is a simultaneous breach", () => {
  const events = [
    health("a", 400, "crit: RTT 210.0ms > 200.0ms"), health("b", 398, "crit: RTT 250.0ms > 200.0ms · Jitter 60.0ms > 50.0ms"),
    health("c", 396, "warn: RTT 60.0ms > 50.0ms"), health("d", 300, "warn: RTT 60.0ms > 50.0ms"),
    health("a", 200, "warn: Jitter 12.0ms > 10.0ms"), health("b", 150, "warn: Jitter 12.0ms > 10.0ms"),
  ];
  const { simultaneous } = lastHourSummary(events, { now: NOW });
  assert.deepEqual(simultaneous, [{ metric: "RTT", hosts: 3, at: ago(400) }]);
});

test("last hour: a list that may not reach back an hour is flagged as partial", () => {
  const recent = Array.from({ length: 10 }, (_, i) => ev("a", i, "ping", "x"));
  assert.equal(lastHourSummary(recent, { now: NOW, limit: 10 }).partial, true, "request limit hit");
  assert.equal(lastHourSummary(recent, { now: NOW, limit: 1000 }).partial, false);
  const ring = Array.from({ length: PER_TARGET_EVENT_CAP }, (_, i) => ev("a", i, "ping", "x"));
  assert.equal(lastHourSummary(ring, { now: NOW, limit: 1000 }).partial, true, "a host's ring is full");
  const old = [...ring, ev("b", 5000, "ping", "x")];
  assert.equal(lastHourSummary(old, { now: NOW, limit: old.length }).partial, false, "the list reaches back past the hour");
});

test("recent events: newest first, identical simultaneous events fold into one line", () => {
  const events = [
    health("a", 400, "ok: Within thresholds"), health("b", 400, "ok: Within thresholds"),
    health("c", 300, "warn: Jitter 12.3ms > 10.0ms"), ev("d", 200, "ping", "Request timeout"),
    { ...ev("d", 250, "ping", "Request timeout"), count: 4, last_at: ago(100) },
  ];
  const out = recentAcrossHosts(events, 3);
  assert.deepEqual(out.map((e) => [e.kind, e.level, e.text, e.hosts]), [
    ["ping", null, "Request timeout ×4", ["d"]],
    ["ping", null, "Request timeout", ["d"]],
    ["health", "warn", "Jitter 12.3 ms", ["c"]],
  ]);
  const grouped = recentAcrossHosts(events, 10).at(-1);
  assert.deepEqual(grouped.hosts.sort(), ["a", "b"]);
  assert.equal(grouped.text, "Within thresholds");
});

test("layout: stacked when the rows fit in half the height, with hysteresis", () => {
  assert.equal(chooseLayout(null, 300, 800), "stacked");
  assert.equal(chooseLayout(null, 450, 800), "side");
  assert.equal(chooseLayout("stacked", 450, 800), "stacked", "between thresholds keeps the current layout");
  assert.equal(chooseLayout("side", 450, 800), "side");
  assert.equal(chooseLayout("stacked", 500, 800), "side");
  assert.equal(chooseLayout("side", 0, 800), "side", "unmeasured keeps the current layout");
  assert.equal(chooseLayout(null, 300, 0), "side");
});
