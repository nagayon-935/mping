import { test } from "node:test";
import assert from "node:assert/strict";
import { healthTransition } from "../static/js/health_text.js";
import { createLog, eventKinds, filterEvents } from "../static/js/log.js";
import { formatEventMessage, formatTime } from "../static/js/time.js";

test("health transitions identify each level from the message prefix", () => {
  for (const [kind, message, expected] of [
    ["health", "ok: Within thresholds", { level: "ok", reason: "Within thresholds" }],
    ["health", "warn:High jitter", { level: "warn", reason: "High jitter" }],
    ["health", "crit: Timeout", { level: "crit", reason: "Timeout" }],
    ["health", "pending: Waiting", { level: "pending", reason: "Waiting" }],
    ["ping", "crit: Timeout", null],
    ["health", "Went crit: Timeout", null],
  ]) assert.deepEqual(healthTransition({ kind, message }), expected);
});

test("event kinds and case-insensitive host filters use the retained events", () => {
  const events = [
    { kind: "ping", host: "Router-A" }, { kind: "health", host: "Router-A" },
    { kind: "route", host: "Router-B" }, { kind: "recovery", host: "example.com" },
  ];
  assert.deepEqual(eventKinds(events), ["", "health", "ping", "recovery", "route"]);
  assert.deepEqual(eventKinds([], "ping"), ["", "health", "ping"]);
  for (const [kind, host, indexes] of [
    ["", "", [0, 1, 2, 3]], ["health", "  ROUTER-a ", [1]], ["", "router", [0, 1, 2]], ["ping", "example", []],
  ]) assert.deepEqual(filterEvents(events, kind, host), indexes.map(i => events[i]));
});

test("repeated events put count and last time in one pair of parentheses", () => {
  const event = { message: "Request timeout", last_at: "2026-11-25T12:10:00+09:00" };
  assert.equal(formatEventMessage({ ...event, count: 1 }), "Request timeout");
  assert.equal(formatEventMessage({ ...event, count: 28 }), `Request timeout (28 times, last ${formatTime(event.last_at)})`);
});

test("Log renders health chips and applies filters with storage blocked", async (t) => {
  function node() {
    return {
      style: {}, children: [], attrs: {}, listeners: {},
      setAttribute(key, value) { this.attrs[key] = value; },
      append(...children) { this.children.push(...children); },
      replaceChildren(...children) { this.children = children; },
      querySelector() { return null; },
      addEventListener(type, listener) { this.listeners[type] = listener; },
    };
  }
  const globals = {
    document: { hidden: false, createElement: node, addEventListener() {} },
    localStorage: {
      getItem() { throw new Error("storage blocked"); },
      setItem() { throw new Error("storage blocked"); },
    },
  };
  for (const [name, value] of Object.entries(globals)) {
    const original = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, { configurable: true, value });
    t.after(() => original ? Object.defineProperty(globalThis, name, original) : delete globalThis[name]);
  }
  t.mock.method(globalThis, "setInterval", (_, interval) => assert.equal(interval, 5000));
  t.mock.method(globalThis, "fetch", async (url) => {
    assert.equal(url, "api/v1/events?limit=200");
    return {
      ok: true,
      json: async () => ({ events: [
        { kind: "health", message: "warn: Jitter", host: "Router-A", at: "2026-11-25T03:10:00Z" },
        { kind: "ping", message: "Request timeout", host: "Router-B", at: "2026-11-25T03:09:59Z" },
      ] }),
    };
  });

  const dom = { body: node(), status: node(), kind: node(), host: node() };
  const drawn = new Promise(resolve => { dom.requestRender = resolve; });
  const log = createLog(dom);
  log.activate(true);
  await drawn;
  log.render();
  const rows = () => dom.body.children[0].children[0].children[2].children;
  assert.match(dom.status.textContent, /Showing 2 of 2/);
  const [healthRow] = rows();
  assert.equal(healthRow.className, "lvl-warn");
  const [chip] = healthRow.children[2].children;
  assert.equal(chip.className, "status lvl-warn");
  assert.equal(chip.children[1].textContent, "health");

  dom.kind.value = "health";
  dom.kind.listeners.change();
  log.render();
  assert.match(dom.status.textContent, /Showing 1 of 2/);
  assert.equal(rows().length, 1);
  dom.host.value = "router-b";
  dom.host.listeners.input();
  log.render();
  assert.match(dom.status.textContent, /Showing 0 of 2/);
  assert.equal(dom.body.children[0].textContent, "No events match these filters.");
});
