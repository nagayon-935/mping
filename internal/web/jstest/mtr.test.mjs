import { test } from "node:test";
import assert from "node:assert/strict";
import { badgeIcon } from "../static/js/dom.js";
import { groupSummary, targetStatus } from "../static/js/model.js";
import { renderMTR } from "../static/js/mtr.js";
import { tracePath } from "../static/js/traceroute.js";

test("status symbols distinguish health, waiting and stopped measurements", () => {
  const symbols = { ok: "○", warn: "△", crit: "×", pending: "–", none: "–", stopped: "◼" };
  for (const [level, symbol] of Object.entries(symbols)) assert.equal(badgeIcon(level), symbol);
  assert.equal(badgeIcon("unknown"), "–");

  for (const status of ["ok", "warn", "crit", "pending"]) {
    assert.equal(targetStatus({ health: { status }, measurement_state: "running" }), status);
  }
  for (const state of ["stopped", "finished"]) {
    assert.equal(targetStatus({ health: { status: "crit" }, measurement_state: state }), "stopped");
  }
});

function createElement(tag) {
  const node = {
    tag,
    children: [],
    attributes: {},
    style: {},
    className: "",
    get cells() { return this.children; },
    get firstElementChild() { return this.children[0] ?? null; },
    get nextElementSibling() {
      const siblings = this.parent?.children;
      return siblings?.[siblings.indexOf(this) + 1] ?? null;
    },
    setAttribute(name, value) { this.attributes[name] = value; },
    insertBefore(child, before) {
      child.remove?.();
      const index = before ? this.children.indexOf(before) : this.children.length;
      this.children.splice(index, 0, child);
      if (child.remove) child.parent = this;
    },
    append(...children) { for (const child of children) this.insertBefore(child, null); },
    remove() {
      if (this.parent) this.parent.children.splice(this.parent.children.indexOf(this), 1);
      this.parent = null;
    },
    replaceWith(child) {
      this.parent.insertBefore(child, this);
      this.remove();
    },
    replaceChildren(...children) {
      this.children = [];
      this.append(...children);
    },
    classList: {
      add(name) { node.className += ` ${name}`; },
      toggle(name, enabled) {
        if (enabled) this.add(name);
        else node.className = node.className.split(" ").filter(part => part !== name).join(" ");
      },
    },
  };
  return node;
}

function mockDocument(t) {
  globalThis.document = { createElement };
  globalThis.getComputedStyle = () => ({ getPropertyValue: () => "100" });
  t.after(() => {
    delete globalThis.document;
    delete globalThis.getComputedStyle;
  });
}

function createMTRTable() {
  const table = Object.assign(createElement("table"), {
    parentElement: { clientWidth: 400, scrollLeft: 0, scrollTop: 0 },
    tHead: createElement("thead"),
    querySelector(selector) { return this.children.find((child) => child.tag === selector); },
  });
  table.append(table.tHead);
  return table;
}

function renderTargetMTR(target) {
  const table = createMTRTable();
  renderMTR(table, [target], { features: {} });
  const [head, ...rows] = table.children.find((child) => child.tag === "tbody").children;
  const summary = head.children[0].children[0].children.find((node) => node.className === "mtr-target-summary");
  return { summary, rows };
}

test("MTR displays raw loss but only marks abnormal effective loss", (t) => {
  mockDocument(t);
  const tests = [
    { ip: "", loss_pct: 100, effective_loss_pct: 0, effective_loss_level: "ok", muted: true },
    { ip: "192.0.2.1", loss_pct: 20, effective_loss_pct: 10, effective_loss_level: "ok", muted: true },
    { ip: "192.0.2.2", loss_pct: 20, effective_loss_pct: 20, effective_loss_level: "ok", muted: false },
    { ip: "", loss_pct: 100, effective_loss_pct: 21, effective_loss_level: "warn", symbol: "△" },
    { ip: "192.0.2.3", loss_pct: 80, effective_loss_pct: 80, effective_loss_level: "warn", symbol: "△" },
    { ip: "", loss_pct: 100, effective_loss_pct: 100, effective_loss_level: "crit", symbol: "×" },
  ];
  const hops = tests.map((hop, i) => ({ ...hop, ttl: i + 1 }));
  const { summary, rows } = renderTargetMTR({ id: 1, host: "example.com", ip: "192.0.2.3",
    health: { status: "warn" }, mtr_hops: hops.map(hop => ({ ...hop, recv: hop.ip ? 1 : 0 })) });
  assert.deepEqual(summary.children.map(node => [node.className, node.textContent]),
    [["mtr-target-count", "6 hops"]]);

  for (const [i, hop] of hops.entries()) {
    const cell = (key) => rows[i].children.find((node) => node.className.split(" ").includes(`col-${key}`));
    const loss = cell("hopLoss");
    const text = hop.loss_pct.toFixed(1);
    if (hop.symbol) {
      const [status] = loss.children;
      assert.equal(status.className, `status lvl-${hop.effective_loss_level}`);
      const [icon, value] = status.children;
      assert.equal(icon.textContent, hop.symbol);
      assert.equal(icon.attributes["aria-hidden"], "true");
      assert.equal(value.textContent, text);
    } else {
      assert.equal(loss.textContent, text);
      assert.equal(loss.children.length, 0);
      assert.equal(loss.className, hop.muted ? "num muted col-hopLoss" : "num col-hopLoss");
    }

    if (!hop.ip) {
      const address = cell("ip");
      assert.equal(address.children[0].textContent, "No reply");
      assert.equal(address.className, hop.muted ? "mono muted col-ip" : "mono col-ip");
    }
  }
});

test("MTR keeps its hop count and discovery messages", (t) => {
  mockDocument(t);
  const target = { host: "example.com", ip: "192.0.2.10", health: { status: "crit" } };
  const partial = [{ ttl: 1, ip: "192.0.2.1", recv: 1, loss_pct: 0 }];
  for (const [hops, error, count, message] of [
    [[], "", "Waiting for hops", "Waiting for hop replies…"],
    [[], "probe failed", "0 hops", "Route discovery failed: probe failed"],
    [partial, "", "1 hop"],
    [partial, "destination unreachable", "1 hop", "Route discovery failed: destination unreachable"],
  ]) {
    const { summary, rows } = renderTargetMTR({ ...target, mtr_hops: hops, mtr_error: error });
    assert.deepEqual(summary.children.map(node => [node.className, node.textContent]),
      [["mtr-target-count", count]]);
    if (message) assert.equal(rows[0].children[0].textContent, message);
    assert.equal(rows.length, hops.length + Number(Boolean(message)));
    if (hops.length) {
      const address = rows.at(-1).children.find(node => node.className.split(" ").includes("col-ip"));
      assert.equal(address.children[0].textContent, hops[0].ip);
    }
  }
});

test("MTR updates only changed hop cells and removes obsolete hops", (t) => {
  mockDocument(t);
  const table = createMTRTable();
  const meta = { features: {} };
  const hop = { ttl: 1, ip: "192.0.2.1", loss_pct: 0, last_rtt_ms: 12 };
  const target = { id: 1, host: "example.com", mtr_hops: [hop, { ...hop, ttl: 2 }] };
  renderMTR(table, [target], meta);
  const body = table.children.find((child) => child.tag === "tbody");
  const [heading, first] = body.children;
  const address = first.cells[1];
  const latest = first.cells[3];

  renderMTR(table, [{ ...target, mtr_hops: [{ ...hop, last_rtt_ms: 25 }] }], meta, target.id);
  assert.equal(table.children.find((child) => child.tag === "tbody"), body);
  assert.equal(body.children.length, 2);
  assert.notEqual(body.children[0], heading); // the displayed hop count changed
  assert.ok(body.children[0].className.includes("is-selected"));
  assert.equal(body.children[1], first);
  assert.equal(first.cells[1], address);
  assert.notEqual(first.cells[3], latest);
  assert.equal(first.cells[3].textContent, "25.0 ms");
});

test("Traceroute keeps hop counts, discovery progress and failures", (t) => {
  mockDocument(t);
  const target = { host: "example.com", ip: "2001:DB8::10", health: { status: "warn" } };
  for (const ip of ["2001:db8::1", "2001:db8::10"]) {
    const path = tracePath({ ...target, mtr_hops: [{ ttl: 1, ip }, { ttl: 2, ip: "" }] }, {});
    const [summary] = path.children;
    assert.equal(summary.className, "trace-summary");
    assert.equal(summary.textContent, "2 hops · 1 no reply");
    assert.equal(summary.children.length, 0);
  }
  for (const [error, level, label] of [
    ["", "pending", "Discovering…"],
    ["probe failed", "warn", "Route discovery failed"],
  ]) {
    const message = tracePath({ ...target, mtr_error: error }, {});
    assert.equal(message.className, "trace-message");
    const [chip, detail] = message.children;
    assert.equal(chip.className, `status lvl-${level}`);
    assert.equal(chip.children[1].textContent, label);
    if (error) assert.equal(detail.textContent, error);
  }
  const partial = tracePath({ ...target, mtr_error: "destination unreachable", mtr_hops: [{ ttl: 1, ip: "2001:db8::1" }] }, {});
  const [warning, summary, hops] = partial.children;
  assert.equal(warning.children[1].textContent, "destination unreachable");
  assert.equal(summary.textContent, "1 hop · 0 no reply");
  assert.equal(hops.children.length, 1);
});

test("group summary text leads with the worst state and omits zero counts", () => {
  for (const [states, expected] of [
    [["ok", "ok"], " · ○ all OK"],
    [["warn", "ok", "crit"], " · × 1 · △ 1"],
    [["finished", "pending", "warn", "warn"], " · △ 2 · – 1 · ◼ 1"],
    [["stopped"], " · ◼ 1"],
  ]) {
    const targets = states.map(state => ({ health: { status: state }, measurement_state: state }));
    const text = groupSummary(targets).map(({ level, label }) => ` · ${badgeIcon(level)} ${label}`).join("");
    assert.equal(text, expected);
  }
});
