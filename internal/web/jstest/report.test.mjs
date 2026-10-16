import { test } from "node:test";
import assert from "node:assert/strict";
import { reportFilename } from "../static/js/report.js";

test("report names keep a single suffix matching the chosen format", () => {
  for (const [input, format, want] of [
    ["daily", "json", "daily.json"],
    ["daily.json", "text", "daily.txt"],
    ["daily.TXT", "json", "daily.json"],
    ["  東京-回線チェック  ", "text", "東京-回線チェック.txt"],
    ["report.2026-10-16.json", "json", "report.2026-10-16.json"],
  ]) assert.deepEqual(reportFilename(input, format), { filename: want });
});

test("report names reject empty names, paths and control characters", () => {
  for (const name of ["", "  ", ".", "..", ".json", "dir/report", "dir\\report", "name\nreport", "name\0report"]) {
    assert.ok(reportFilename(name, "json").error, name);
  }
  assert.ok(reportFilename("daily", "csv").error);
});
