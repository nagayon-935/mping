import { test } from "node:test";
import assert from "node:assert/strict";
import { connectionSummary, explainBreach, parseBreaches, shortReason, ttlHops, withUnit } from "../static/js/health_text.js";
import { commonCauseText, detectCommonCause, formatRange } from "../static/js/common_cause.js";
import { describeEvent, latestEvents } from "../static/js/inspect_events.js";
import { latestProbeAt, measurementState } from "../static/js/header.js";

test("shortReason keeps what breached and its value, dropping the threshold", () => {
  assert.equal(shortReason("Jitter 50.5ms > 50.0ms"), "Jitter 50.5 ms");
  assert.equal(shortReason("Recent loss 25.0% > 5.0% · RTT 220.0ms > 200.0ms"), "Loss 25.0% · RTT 220.0 ms");
  assert.equal(shortReason("29 consecutive failures"), "29 consecutive failures");
  assert.equal(shortReason("Recovering (1/2 replies)"), "Recovering (1/2 replies)");
  assert.equal(shortReason(""), "");
  assert.equal(shortReason(undefined), "");
});

test("parseBreaches reads every breach and ignores other reasons", () => {
  assert.deepEqual(parseBreaches("Recent loss 25.0% > 5.0% · Jitter 12.3ms > 10.0ms · Waiting"), [
    { metric: "Loss", value: 25, threshold: 5, unit: "%" },
    { metric: "Jitter", value: 12.3, threshold: 10, unit: "ms" },
  ]);
  assert.deepEqual(parseBreaches("3 consecutive failures"), []);
  assert.deepEqual(parseBreaches(null), []);
});

test("explainBreach names the threshold level from the crit threshold", () => {
  const th = { jitter_crit_ms: 50, jitter_warn_ms: 10, rtt_crit_ms: 200, rtt_warn_ms: 50 };
  assert.deepEqual(explainBreach({ metric: "Jitter", value: 50.5, threshold: 50, unit: "ms" }, th),
    { lead: "Jitter 50.5 ms", rest: "is over the 50.0 ms critical threshold" });
  assert.equal(explainBreach({ metric: "Jitter", value: 12.3, threshold: 10, unit: "ms" }, th).rest,
    "is over the 10.0 ms warning threshold");
  // Group-specific thresholds: levels from the server override global thresholds.
  assert.equal(explainBreach({ metric: "RTT", value: 120, threshold: 100, unit: "ms" }, th, { last_rtt: "crit" }).rest,
    "is over the 100.0 ms critical threshold");
  assert.equal(withUnit(5, "%"), "5.0%");
});

test("ttlHops assumes the nearest common initial TTL", () => {
  assert.equal(ttlHops(114), 14);
  assert.equal(ttlHops(57), 7);
  assert.equal(ttlHops(64), 0);
  assert.equal(ttlHops(250), 5);
  assert.equal(ttlHops(0), null);
  assert.equal(ttlHops(undefined), null);
});

test("connectionSummary folds connection, server and measurement state", () => {
  const live = { state: "running", connected: true, measurement: "running", hasSnapshot: true };
  assert.equal(connectionSummary(live).label, "Live");
  assert.equal(connectionSummary({ ...live, measurement: "stopped" }).label, "Stopped");
  assert.equal(connectionSummary({ ...live, measurement: "finished" }).label, "Finished");
  assert.equal(connectionSummary({ ...live, connected: false }).label, "Reconnecting");
  assert.match(connectionSummary({ ...live, connected: false }).detail, /last received values/);
  assert.equal(connectionSummary({ state: null, connected: false, hasSnapshot: false }).label, "Connecting");
  assert.equal(connectionSummary({ ...live, state: "stopped", connected: false }).level, "stopped");
  assert.equal(connectionSummary({ ...live, state: "reloading" }).level, "warn");
});

const host = (status, reason, extra = {}) => ({ health: { status, reason, unavailable: false, ...extra } });
const jitter = (v) => host(v > 50 ? "crit" : "warn", `Jitter ${v.toFixed(1)}ms > ${v > 50 ? "50.0" : "10.0"}ms`);

test("common cause: most answering hosts breaching one metric is reported once", () => {
  const targets = [
    host("crit", "29 consecutive failures", { unavailable: true }),
    jitter(50.5), jitter(50.1), jitter(48.1), jitter(47.4),
  ];
  const cause = detectCommonCause(targets);
  assert.deepEqual(cause, { metric: "Jitter", unit: "ms", count: 4, answering: 4, total: 5, min: 47.4, max: 50.5 });
  assert.equal(commonCauseText(cause).headline, "Common cause: Jitter over threshold on 4 / 5 hosts (47–51 ms)");
});

test("common cause needs at least 3 answering hosts", () => {
  assert.equal(detectCommonCause([jitter(20), jitter(30)]), null);
  assert.equal(detectCommonCause([jitter(20), jitter(30), host("pending", "Waiting for a completed probe")]), null);
  assert.ok(detectCommonCause([jitter(20), jitter(30), jitter(40)]));
});

test("common cause needs at least 75% of answering hosts", () => {
  const ok = () => host("ok", "Within thresholds");
  assert.equal(detectCommonCause([jitter(20), jitter(30), ok(), ok()]), null, "50%");
  assert.ok(detectCommonCause([jitter(20), jitter(30), jitter(25), ok()]), "75%");
  assert.equal(detectCommonCause([jitter(20), jitter(30), jitter(25), ok(), ok()]), null, "60%");
});

test("common cause requires the same metric, and picks the most shared one", () => {
  const rtt = host("warn", "RTT 220.0ms > 200.0ms");
  assert.equal(detectCommonCause([jitter(20), jitter(30), rtt, rtt]), null);
  const both = host("crit", "RTT 250.0ms > 200.0ms · Jitter 60.0ms > 50.0ms");
  const cause = detectCommonCause([both, both, rtt, jitter(20)]);
  assert.equal(cause.metric, "RTT");
  assert.equal(cause.count, 3);
  assert.equal(detectCommonCause(null), null);
});

test("formatRange rounds from 10 up and keeps a decimal below", () => {
  assert.equal(formatRange(47.4, 50.5, "ms"), "47–51 ms");
  assert.equal(formatRange(48.2, 48.4, "ms"), "48 ms");
  assert.equal(formatRange(1.24, 3, "ms"), "1.2–3.0 ms");
  assert.equal(formatRange(25, 40, "%"), "25–40%");
});

test("latestEvents orders repeated events by last_at and caps at limit", () => {
  const events = [
    { at: "2026-10-16T08:00:00Z", last_at: "2026-10-16T08:05:00Z", count: 4, kind: "health", message: "crit: Jitter 55.0ms > 50.0ms" },
    { at: "2026-10-16T08:01:00Z", kind: "health", message: "warn: RTT 120.0ms > 100.0ms" },
    { at: "2026-10-16T08:03:00Z", kind: "ip", message: "IP changed" },
  ];
  assert.deepEqual(latestEvents(events, 2).map((e) => e.at), [
    "2026-10-16T08:00:00Z",
    "2026-10-16T08:03:00Z",
  ]);
  const d = describeEvent(events[0]);
  assert.equal(d.level, "crit");
  assert.match(d.text, /^Jitter 55\.0ms > 50\.0ms ×4 \(last /);
});

test("latestProbeAt compares parsed timestamps rather than RFC3339Nano text", () => {
  const targets = [
    { health: { last_probe_at: "2026-10-16T08:30:00Z" } },
    { health: { last_probe_at: "2026-10-16T08:30:00.1Z" } },
  ];
  assert.equal(latestProbeAt(targets), "2026-10-16T08:30:00.1Z");
  assert.equal(latestProbeAt([]), undefined);
  assert.equal(measurementState({ snapshot: {} }), undefined);
});
