// Browser checks for the dashboard against the simulated server. Not part of
// CI (it needs a Chromium); run it by hand after UI changes:
//
//   MPING_WEB_DEV_PORT=8090 MPING_WEB_DEV_TOKEN=e2e-token MPING_WEB_DEV_UNTRUSTED=1 \
//     go test -run TestDevServer -timeout 0 ./internal/web &
//   E2E_TOKEN=e2e-token PLAYWRIGHT_CORE=/path/to/node_modules/playwright-core \
//     [CHROMIUM_PATH=/path/to/chrome] node internal/web/jstest/e2e.mjs http://127.0.0.1:8090/
//
// Scope selectors to #targets: the HTTP table reuses col-sent/col-max.
// The control checks add, delete and reset on the simulator, so restart it
// before a re-run.
//
// Exits non-zero on the first failed check. Screenshots go to $E2E_OUT when set.
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const base = process.argv[2] ?? "http://127.0.0.1:8090/";
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.PLAYWRIGHT_CORE ?? "playwright-core");
const out = process.env.E2E_OUT;
const shot = async (page, name) => out && page.screenshot({ path: `${out}/${name}.png` });

const browser = await chromium.launch(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {});
const problems = [];
let dialogs = 0;

async function open(opts, context = browser, url = base) {
  // BrowserContext.newPage takes no options, so size those pages afterwards.
  const page = context === browser ? await browser.newPage(opts) : await context.newPage();
  if (context !== browser && opts.viewport) await page.setViewportSize(opts.viewport);
  page.on("console", (m) => {
    // The duplicate-host check provokes a 409 on purpose; the browser logs
    // every non-2xx fetch as a console error.
    if (/status of (409|403)/.test(m.text())) return;
    if (["error", "warning"].includes(m.type())) problems.push(m.text());
  });
  page.on("pageerror", (e) => problems.push(e.message));
  page.on("dialog", async (d) => { dialogs++; await d.dismiss(); });
  await page.goto(url);
  await page.waitForSelector("tr.target-row");
  await page.waitForTimeout(2500); // first sparkline refresh
  return page;
}

const check = async (name, fn) => {
  await fn();
  console.log(`ok - ${name}`);
};

try {
  for (const colorScheme of ["light", "dark"]) {
    const page = await open({ viewport: { width: 1400, height: 900 }, colorScheme });
    await shot(page, `main-${colorScheme}`);
    await page.close();
  }

  // Wide enough that the ping monitor pane (left of the RTT graphs) fits
  // every column; narrower layouts are checked separately below.
  const WIDE = { width: 4200, height: 1000 };
  const page = await open({ viewport: WIDE });

  await check("live badge and grouped rows", async () => {
    assert.match(await page.textContent("#badge"), /Live/);
    assert.match(await page.textContent("#updated"), /^Last change /);
    assert.deepEqual(await page.$$eval("tr.group-row", (r) => r.map((x) => x.textContent)), ["core (2)", "internet (3)", "Ungrouped (1)"]);
    assert.equal(await page.$$eval("tr.target-row", (r) => r.length), 6);
    assert.ok(await page.isVisible('#inspect-tabs [data-tab="http"]'));
    assert.equal(await page.$$eval(".graph-card", (n) => n.length), 6);
  });

  await check("a wide screen shows every column; a narrow one keeps the highest-priority ones", async () => {
    const headers = () => page.$$eval("#targets th", (h) => h.map((x) => x.textContent));
    assert.deepEqual(await headers(),
      ["Status", "Host", "IP", "AS", "Loss %", "Sent", "Recv", "Last", "Avg", "Min", "Peak", "Jitter", "TTL", "RTT trend"]);
    assert.equal(await page.$$eval("input[type=checkbox]", (n) => n.length), 0, "no column toggle any more");
    // Spare width is turned into a larger table, not an empty strip: the table
    // fills its wrapper, and the host column stays a modest share of it.
    const fit = await page.$eval("#targets", (t) => ({
      table: t.getBoundingClientRect().width, wrap: t.parentElement.clientWidth, zoom: Number(t.style.zoom || 1),
      host: t.querySelector("th.col-host").getBoundingClientRect().width,
    }));
    assert.ok(Math.abs(fit.table - fit.wrap) <= 2, `table ${fit.table}px should fill its ${fit.wrap}px wrapper`);
    assert.ok(fit.host / fit.table <= 0.2, `host column is ${Math.round((fit.host / fit.table) * 100)}% of the table`);
    assert.match(await page.locator("tr.target-row").nth(0).locator(".col-last").textContent(), / ms$/);
    assert.equal(await page.locator("tr.target-row").nth(4).locator(".col-last").textContent(), "–");
    assert.match(await page.locator("tr.target-row").nth(4).textContent(), /No replies received/);
    assert.equal(await page.locator("td.col-max.lvl-crit, td.col-max.lvl-warn, td.col-avg.lvl-crit, td.col-avg.lvl-warn").count(), 0);

    await page.setViewportSize({ width: 900, height: 1000 });
    await page.waitForSelector("#targets th.col-sent", { state: "detached" });
    const narrow = await headers();
    assert.deepEqual(narrow.slice(0, 2), ["Status", "Host"]);
    for (const kept of ["Loss %", "Last", "Avg", "Jitter"]) assert.ok(narrow.includes(kept), `${kept} must survive narrowing: ${narrow}`);
    for (const dropped of ["IP", "AS", "Sent", "Recv", "TTL"]) assert.ok(!narrow.includes(dropped), `${dropped} should drop first: ${narrow}`);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);

    await page.setViewportSize(WIDE);
    await page.waitForSelector("#targets th.col-sent");
  });

  await check("untrusted names render as text", async () => {
    const hosts = await page.$$eval(".host", (h) => h.map((x) => x.textContent));
    assert.ok(hosts.includes("<script>alert(1)</script>"));
    assert.equal(await page.$$eval("img", (i) => i.length), 0);
    assert.equal(dialogs, 0);
  });

  await check("live measurement updates keep column widths and row positions fixed", async () => {
    const geometry = () => page.evaluate(() => ({
      widths: [...document.querySelectorAll('#targets th')].map((n) => n.getBoundingClientRect().width),
      rows: [...document.querySelectorAll('#targets tr.target-row')].map((n) => [n.offsetTop, n.getBoundingClientRect().height]),
    }));
    const before = await geometry();
    const changedAt = await page.textContent("#updated");
    await page.waitForFunction((text) => document.querySelector('#updated').textContent !== text, changedAt);
    assert.deepEqual(await geometry(), before);
  });

  await check("text selection survives live updates", async () => {
    const before = await page.evaluate(() => {
      const cell = document.querySelector("tr.target-row td.col-ip");
      const range = document.createRange();
      range.selectNodeContents(cell);
      getSelection().removeAllRanges();
      getSelection().addRange(range);
      return getSelection().toString();
    });
    await page.waitForTimeout(2500);
    const after = await page.evaluate(() => getSelection().toString());
    assert.equal(after, before);
    const sent = () => page.$eval("tr.target-row td.col-sent", (td) => Number(td.title.replaceAll(",", "")));
    const held = await sent();
    await page.evaluate(() => getSelection().removeAllRanges());
    await page.waitForTimeout(1500);
    assert.ok((await sent()) > held, "table resumes updating once the selection is cleared");
  });

  await check("selecting a target drives the monitor, graphs and inspect panes", async () => {
    const monitor = await page.locator("#pane-monitor").boundingBox();
    const graphs = await page.locator("#pane-graphs").boundingBox();
    const inspect = await page.locator("#pane-inspect").boundingBox();
    assert.ok(monitor.x + monitor.width <= graphs.x + 1, "monitor and graphs sit side by side on a wide screen");
    assert.ok(inspect.y >= monitor.y + monitor.height - 1, "inspect pane sits below them");

    await page.click("tr.target-row >> nth=2");
    await page.waitForFunction(() => document.querySelector("#inspect-target").textContent === "for cdn.example");
    const id = await page.$eval('tr.target-row[aria-current="true"]', (r) => r.dataset.targetId);
    assert.equal(await page.$$eval('tr.target-row[aria-current="true"]', (r) => r.length), 1);
    assert.equal(await page.$$eval("tr.target-row[aria-selected]", (r) => r.length), 0);
    assert.equal(await page.$eval('.graph-card[aria-pressed="true"]', (c) => c.dataset.targetId), id);
    assert.equal(await page.evaluate(() => location.hash), `#target-${id}`);

    assert.equal(await page.textContent('#inspect-tabs [aria-selected="true"]'), "Summary");
    assert.deepEqual(await page.$$eval("#inspect-body .detail-section h3", (h) => h.map((x) => x.textContent)), ["Current", "Since start / reset"]);
    assert.equal(await page.$eval("#inspect-body", (n) => n.textContent.includes("null")), false);
    await page.click('#inspect-tabs [data-tab="path"]');
    assert.ok((await page.$$eval("#inspect-body th", (h) => h.map((x) => x.textContent))).includes("Hop"));
    await shot(page, "panes");
  });

  await check("hovering one RTT graph moves a shared cursor across all of them", async () => {
    const box = await page.locator(".graph-card canvas >> nth=0").boundingBox();
    await page.mouse.move(box.x + box.width - 20, box.y + box.height / 2);
    await page.waitForFunction(() => document.querySelector("#graphs-cursor").textContent.startsWith("Cursor:"));
    const values = await page.$$eval(".graph-value", (n) => n.map((x) => x.textContent));
    assert.equal(values.length, 6);
    assert.ok(values.every((v) => /ms$|^Lost$|^–$/.test(v)), `cursor readouts: ${values}`);
    await page.mouse.move(0, 0);
    await page.waitForFunction(() => !document.querySelector("#graphs-cursor").textContent.startsWith("Cursor:"));
    await page.click('#scale-shared');
    assert.equal(await page.getAttribute("#scale-shared", "aria-pressed"), "true");
    await page.click('#scale-each');
  });

  await check("the log tab lists every target's events newest first; a host link selects it", async () => {
    await page.click('#inspect-tabs [data-tab="log"]');
    await page.waitForSelector("#inspect-body table.log tbody tr");
    const rows = await page.$$eval("#inspect-body table.log tbody tr", (rs) => rs.map((r) => ({
      at: r.querySelector("time").getAttribute("datetime"), host: r.querySelector(".link").textContent })));
    assert.ok(new Set(rows.map((r) => r.host)).size >= 2, "events from more than one target");
    assert.deepEqual(rows.map((r) => r.at), [...rows.map((r) => r.at)].sort().reverse(), "newest first");
    await page.locator("#inspect-body .link", { hasText: "flaky.example" }).first().click();
    await page.waitForFunction(() => document.querySelector('tr.target-row[aria-current="true"] .host')?.textContent === "flaky.example");
    await page.getByRole("button", { name: "Selected target only" }).click();
    const hosts = await page.$$eval("#inspect-body table.log .link", (n) => n.map((x) => x.textContent));
    assert.ok(hosts.length > 0 && hosts.every((h) => h === "flaky.example"), `filtered hosts: ${hosts}`);
    await page.getByRole("button", { name: "Show all targets" }).click();
  });

  await check("ports tab; Escape clears the selection; j and k move it", async () => {
    await page.click("tr.target-row >> nth=0");
    await page.click('#inspect-tabs [data-tab="ports"]');
    assert.match(await page.textContent("#inspect-body"), /443\/tcp/);
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => document.querySelector("#inspect-target").textContent === "");
    assert.match(await page.textContent("#inspect-body"), /Select a target/);
    assert.equal(await page.$$eval('tr.target-row[aria-current="true"]', (r) => r.length), 0);

    await page.click("tr.target-row >> nth=0");
    await page.keyboard.press("j");
    await page.waitForFunction(() => document.querySelector('tr.target-row[aria-current="true"] .host')?.textContent === "core-rtr2.example");
    await page.keyboard.press("k");
    await page.waitForFunction(() => document.querySelector('tr.target-row[aria-current="true"] .host')?.textContent === "core-rtr1.example");
  });

  await check("Escape in the filter field keeps the selection", async () => {
    await page.fill("#filter", "core");
    await page.focus("#filter");
    await page.keyboard.press("Escape");
    await page.waitForTimeout(200);
    assert.equal(await page.$$eval('tr.target-row[aria-current="true"]', (r) => r.length), 1);
    await page.fill("#filter", "");
    await page.keyboard.press("Escape");
  });

  await check("filter narrows the table", async () => {
    const widths = () => page.$$eval("#targets th", (ns) => ns.map((n) => n.getBoundingClientRect().width));
    const before = await widths();
    await page.fill("#filter", "flaky");
    await page.waitForTimeout(300);
    assert.equal(await page.$$eval("tr.target-row", (r) => r.length), 1);
    assert.deepEqual(await widths(), before, "removing the scrollbar must not resize the columns");
    await page.fill("#filter", "");
  });
  await page.close();

  await check("without a token the dashboard is read-only", async () => {
    const ro = await open({ viewport: { width: 1400, height: 900 } });
    assert.ok(await ro.isVisible("#readonly-hint"));
    assert.ok(await ro.isHidden("#add-form"));
    assert.ok(await ro.isHidden("#reset-slot"));
    await ro.click("tr.target-row >> nth=0");
    await ro.waitForFunction(() => document.querySelector("#inspect-target").textContent.startsWith("for "));
    assert.equal(await ro.$$eval('#inspect-body [data-section="actions"]', (n) => n.length), 0);
    await ro.click("#readonly-hint");
    assert.ok(await ro.isVisible("#readonly-explanation"));
    assert.match(await ro.textContent("#readonly-explanation"), /Log pane/);
    await ro.close();
  });

  const token = process.env.E2E_TOKEN;
  assert.ok(token, "set E2E_TOKEN to the simulator's MPING_WEB_DEV_TOKEN");
  const ctx = await browser.newContext();
  const ctl = await open({ viewport: WIDE }, ctx, `${base}#token=${token}`);
  const rowHosts = () => ctl.$$eval("tr.target-row .host", (h) => h.map((x) => x.textContent));

  await check("the token link enables controls and leaves the address bar", async () => {
    await ctl.waitForSelector("#add-form:not([hidden])");
    assert.equal(await ctl.evaluate(() => location.hash), "");
    assert.ok(await ctl.isHidden("#readonly-hint"));
    await shot(ctl, "control");
  });

  await check("adding a host: client-side validation, then a live row", async () => {
    await ctl.fill("#add-host", "bad host");
    await ctl.click("#add-form button");
    assert.match(await ctl.textContent("#notice"), /spaces/);
    await ctl.fill("#add-host", "added.e2e.example");
    await ctl.click("#add-form button");
    await ctl.waitForFunction(() => [...document.querySelectorAll(".host")].some((h) => h.textContent === "added.e2e.example"), null, { timeout: 5000 });
    assert.match(await ctl.textContent("#notice"), /Added added\.e2e\.example/);
    assert.equal(await ctl.inputValue("#add-host"), "");
  });

  await check("a double submit adds once without a spurious error", async () => {
    await ctl.fill("#add-host", "double.e2e.example");
    await ctl.evaluate(() => {
      const form = document.getElementById("add-form");
      form.requestSubmit();
      form.requestSubmit();
    });
    await ctl.waitForFunction(() => [...document.querySelectorAll(".host")].some((h) => h.textContent === "double.e2e.example"), null, { timeout: 5000 });
    await ctl.waitForTimeout(800);
    const isError = await ctl.$eval("#notice", (n) => n.classList.contains("is-error"));
    assert.ok(!isError, `unexpected error notice: ${await ctl.textContent("#notice")}`);
  });

  await check("server-side rejections are shown", async () => {
    await ctl.fill("#add-host", "added.e2e.example");
    await ctl.click("#add-form button");
    await ctl.waitForFunction(() => document.querySelector("#notice").classList.contains("is-error"));
    assert.match(await ctl.textContent("#notice"), /already in the list/);
  });

  await check("deleting needs a confirming second press", async () => {
    const row = ctl.locator("tr.target-row", { hasText: "added.e2e.example" });
    await row.click();
    await ctl.click('#inspect-tabs [data-tab="summary"]');
    const del = ctl.getByRole("button", { name: "Delete added.e2e.example" });
    await del.click();
    assert.ok((await rowHosts()).includes("added.e2e.example"), "first press must not delete");
    await ctl.getByRole("button", { name: "Confirm delete added.e2e.example?" }).click();
    await ctl.waitForFunction(() => ![...document.querySelectorAll(".host")].some((h) => h.textContent === "added.e2e.example"), null, { timeout: 5000 });
    assert.equal(await ctl.textContent("#inspect-target"), "", "the deleted target is no longer selected");
  });

  await check("reset clears counters after confirmation", async () => {
    const sent = () => ctl.$eval("tr.target-row td.col-sent", (td) => Number(td.title.replaceAll(",", "")));
    const before = await sent();
    await ctl.getByRole("button", { name: "Reset stats" }).click();
    await ctl.getByRole("button", { name: "Confirm reset?" }).click();
    await ctl.waitForFunction((b) => Number(document.querySelector("tr.target-row td.col-sent").title.replaceAll(",", "")) < b, before, { timeout: 5000 });
    assert.match(await ctl.textContent("#notice"), /Statistics reset/);
  });

  await check("a token rejected mid-session retracts every control", async () => {
    const other = await browser.newContext();
    const page2 = await open({ viewport: { width: 1400, height: 900 } }, other, `${base}#token=${token}`);
    await page2.waitForSelector("#add-form:not([hidden])");
    await page2.route("**/api/v1/targets/*", (route) =>
      route.request().method() === "DELETE"
        ? route.fulfill({ status: 403, contentType: "application/json", body: '{"error":"control token missing or invalid"}' })
        : route.continue());
    await page2.click("tr.target-row >> nth=0");
    await page2.getByRole("button", { name: /^Delete / }).click();
    await page2.getByRole("button", { name: /^Confirm delete / }).click();
    await page2.waitForFunction(() => document.querySelector("#add-form").hidden);
    assert.ok(await page2.isVisible("#readonly-hint"));
    assert.equal(await page2.$$eval('#inspect-body [data-section="actions"]', (n) => n.length), 0);
    await other.close();
  });

  await check("opening via localhost explains why the page is read-only", async () => {
    const lh = await browser.newContext();
    const page3 = await open({ viewport: { width: 1400, height: 900 } }, lh, base.replace("127.0.0.1", "localhost"));
    assert.ok(await page3.isVisible("#readonly-hint"));
    assert.match(await page3.getAttribute("#readonly-hint", "title"), /127\.0\.0\.1/);
    await lh.close();
  });

  await check("a stale token falls back to read-only", async () => {
    const stale = await browser.newContext();
    const page = await open({ viewport: { width: 1400, height: 900 } }, stale, `${base}#token=not-the-token`);
    await page.waitForTimeout(500);
    assert.ok(await page.isVisible("#readonly-hint"));
    assert.ok(await page.isHidden("#add-form"));
    await stale.close();
  });

  const mobile = await open({ viewport: { width: 390, height: 844 } }, ctx);
  await check("phone width keeps status, host and the highest-priority columns without page overflow", async () => {
    assert.ok(await mobile.isVisible("#add-form"));
    assert.equal(await mobile.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    const headers = await mobile.$$eval("#targets th", (h) => h.map((x) => x.textContent));
    assert.deepEqual(headers.slice(0, 2), ["Status", "Host"]);
    for (const kept of ["Loss %", "Last"]) assert.ok(headers.includes(kept), `${kept} must stay on a phone: ${headers}`);
    for (const dropped of ["IP", "AS", "Sent", "Recv", "TTL"]) assert.ok(!headers.includes(dropped), `${dropped} should be dropped: ${headers}`);
    assert.ok(await mobile.$eval("#targets", (n) => n.getBoundingClientRect().width <= n.parentElement.clientWidth + 1));
    await shot(mobile, "mobile");
  });

  await check("no console errors", async () => assert.deepEqual(problems, []));
} finally {
  await browser.close();
}
