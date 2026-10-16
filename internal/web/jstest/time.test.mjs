import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { formatTime, formatDateTime } from "../static/js/time.js";
import { targetColumns } from "../static/js/table.js";

for (const [zone, time, date] of [
  ["Asia/Tokyo", "00:05:06", "2026-10-17"],
  ["America/New_York", "11:05:06", "2026-10-16"],
  ["UTC", "15:05:06", "2026-10-16"],
]) test(`24h dates follow the system timezone ${zone}`, () => {
  const module = new URL("../static/js/time.js", import.meta.url).href;
  const code = `import {formatTime,formatDateTime} from ${JSON.stringify(module)};console.log(JSON.stringify([formatTime("2026-10-16T15:05:06Z"),formatDateTime("2026-10-16T15:05:06Z")]));`;
  const actual = JSON.parse(execFileSync(process.execPath, ["--input-type=module", "-e", code], {env: {...process.env, TZ: zone},encoding:"utf8"}));
  assert.deepEqual(actual, [time, `${date} ${time}`]);
});

test("missing times do not turn into an epoch timestamp", () => {
  assert.equal(formatTime(null), "–");
  assert.equal(formatDateTime("invalid"), "–");
});

test("overview retains minimum fields, feature flags and current RTT only", () => {
  const columns = targetColumns({asn:false,dscp:false});
  assert.deepEqual(columns.filter(c=>c.required).map(c=>c.key), ["status","host","loss","last"]);
  assert.ok(!columns.some(c=>["asn","dscp","avg","min","max","spark"].includes(c.key)));
  assert.ok(targetColumns({asn:true,dscp:true}).some(c=>c.key==="asn"));
  assert.ok(targetColumns({asn:true,dscp:true}).some(c=>c.key==="dscp"));
});
