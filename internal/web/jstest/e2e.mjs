// Browser checks for the dashboard against the simulated server. Not part of
// CI (it needs a Chromium); run it by hand after UI changes:
//
//   MPING_WEB_DEV_PORT=8090 MPING_WEB_DEV_TOKEN=e2e-token-0123456789 MPING_WEB_DEV_UNTRUSTED=1 \
//     go test -run TestDevServer -timeout 0 ./internal/web &
//   E2E_TOKEN=e2e-token-0123456789 PLAYWRIGHT_CORE=/path/to/node_modules/playwright-core \
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

async function open(opts, context = browser, url = base, snapshot) {
  // BrowserContext.newPage takes no options, so size those pages afterwards.
  const page = context === browser ? await browser.newPage(opts) : await context.newPage();
  if (context !== browser && opts.viewport) await page.setViewportSize(opts.viewport);
  // Observe the actual rendered axis labels, not just the scale controls.
  await page.addInitScript(() => {
    const proto = CanvasRenderingContext2D.prototype;
    const clear = proto.clearRect, fill = proto.fillText;
    proto.clearRect = function (...args) { this.canvas.axisLabels = []; return clear.apply(this, args); };
    proto.fillText = function (text, ...args) {
      if (/^\d+(\.\d+)?$/.test(String(text))) (this.canvas.axisLabels ??= []).push(String(text));
      return fill.call(this, text, ...args);
    };
  });
  page.on("console", (m) => {
    // The duplicate-host check provokes a 409 on purpose; the browser logs
    // every non-2xx fetch as a console error.
    if (/status of (409|403)/.test(m.text())) return;
    if (/status of 500/.test(m.text()) && /\/api\/v1\/report\?/.test(m.location().url)) return;
    if (["error", "warning"].includes(m.type())) problems.push(m.text());
  });
  page.on("pageerror", (e) => problems.push(e.message));
  page.on("dialog", async (d) => { dialogs++; await d.dismiss(); });
  if (snapshot) await page.route("**/api/v1/stream", (route) => route.fulfill({
    contentType: "text/event-stream", body: `event: snapshot\ndata: ${JSON.stringify(snapshot)}\n\n`,
  }));
  await page.goto(url);
  await page.waitForSelector(new URL(url).hash === "#log" ? "#page-log:not([hidden])" : "tr.target-row", {state:"attached"});
  await page.waitForTimeout(2500); // first history refresh
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

  // Extra-wide windows also exercise the workspace maximum width.
  // Narrower layouts are checked separately below.
  const WIDE = { width: 4200, height: 1000 };
  // The narrowest supported window (style.css: body min-width).
  const NARROWEST = { width: 720, height: 900 };
  const page = await open({ viewport: WIDE });

  await check("live badge and grouped rows", async () => {
    assert.match(await page.textContent("#badge"), /Online/);
    assert.match(await page.textContent("#updated"), /^Last probe /);
    assert.deepEqual(await page.$$eval("tr.group-row", (r) => r.map((x) => x.textContent)), ["core (2)", "internet (3)", "Ungrouped (1)"]);
    assert.equal(await page.$$eval("tr.target-row", (r) => r.length), 6);
    assert.ok(await page.isHidden("#pane-inspect"));
    assert.ok(await page.isVisible("#pane-mtr"));
    assert.equal(await page.locator("#mtr tbody").count(),6);
    assert.equal(await page.textContent("#mtr-summary"),"6 hosts · ICMP");
    assert.ok(await page.isVisible("#nav-http"));
    assert.equal(await page.locator('#inspect-tabs [data-tab="summary"]').count(), 0);
    assert.equal(await page.$$eval(".graph-card", (n) => n.length), 6);
  });

  await check("graphs default to identical rendered RTT ticks across all targets", async () => {
    assert.equal(await page.getAttribute("#scale-shared", "aria-pressed"), "true");
    const ticks = await page.$$eval(".graph-canvas", nodes => nodes.map(n => n.axisLabels));
    assert.equal(ticks[0].length, 5);
    for (const axis of ticks) assert.deepEqual(axis, ticks[0]);
  });

  await check("a wide screen shows every column; a narrow one keeps the highest-priority ones", async () => {
    const headers = () => page.$$eval("#targets th", (h) => h.map((x) => x.textContent));
    assert.deepEqual(await headers(),
      ["Status", "Host", "IP", "AS", "Loss %", "Sent", "Recv", "RTT", "Jitter", "TTL"]);
    assert.equal(await page.$$eval("#pane-monitor input[type=checkbox]", (n) => n.length), 0, "no column toggle any more");
    // Live values never change column widths. Columns respond only to available width.
    const fit = await page.$eval("#targets", (t) => ({
      table: t.getBoundingClientRect().width, wrap: t.parentElement.clientWidth, zoom: Number(t.style.zoom || 1),
      host: t.querySelector("th.col-host").getBoundingClientRect().width,
    }));
    assert.equal(fit.zoom, 1, "table does not use automatic zoom");
    assert.ok(fit.table <= fit.wrap + 2, "table fits its wrapper");
    assert.ok(Math.abs(fit.table - fit.wrap) <= 1, "table fills its wrapper");
    assert.ok(fit.host >= 200, "identity column retains its minimum width");
    assert.ok(await page.$eval("#workspace", (n) => n.getBoundingClientRect().width === innerWidth), "workspace uses the full browser width");
    assert.match(await page.locator("tr.target-row").nth(0).locator(".col-last").textContent(), / ms$/);
    assert.equal(await page.locator("tr.target-row").nth(4).locator(".col-last").textContent(), "–");
    assert.match(await page.locator("tr.target-row").nth(4).textContent(), /consecutive failures/);
    assert.equal(await page.locator("td.col-max.lvl-crit, td.col-max.lvl-warn, td.col-avg.lvl-crit, td.col-avg.lvl-warn").count(), 0);

    await page.setViewportSize({ width: 900, height: 1000 });
    await page.waitForSelector("#targets th.col-asn", { state: "detached" });
    const narrow = await headers();
    assert.deepEqual(narrow.slice(0, 2), ["Status", "Host"]);
    for (const kept of ["Loss %", "RTT", "Jitter"]) assert.ok(narrow.includes(kept), `${kept} must survive narrowing: ${narrow}`);
    for (const dropped of ["IP", "AS", "TTL"]) assert.ok(!narrow.includes(dropped), `${dropped} should drop first: ${narrow}`);
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);

    await page.setViewportSize(WIDE);
    await page.waitForSelector("#targets th.col-asn");
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

  await check("selection opens only Summary in a viewport-filling workspace", async () => {
    await page.click("tr.target-row >> nth=2");
    await page.waitForSelector("#pane-inspect:not([hidden])");
    assert.equal(await page.textContent("#inspect-title"), "Summary");
    assert.equal(await page.locator('#inspect-tabs, #inspect-body [role="tab"]').count(), 0);
    await page.waitForFunction(() => document.querySelector("#inspect-target").textContent === "cdn.example");
    assert.equal(await page.getAttribute('.graph-card[data-target-id="3"]', "aria-pressed"), "true");
    const geometry = await page.evaluate(() => {
      const rect = id => { const r=document.getElementById(id).getBoundingClientRect(); return {x:r.x,y:r.y,right:r.right,bottom:r.bottom,width:r.width,height:r.height}; };
      return {monitor:rect("pane-monitor"),summary:rect("pane-inspect"),route:rect("pane-mtr"),graphs:rect("pane-graphs"),work:rect("workspace"),w:innerWidth,h:innerHeight,scroll:document.documentElement.scrollHeight};
    });
    const {monitor,summary,route,graphs,work,w,h,scroll}=geometry;
    assert.equal(work.width,w,"workspace uses full browser width");
    assert.ok(monitor.right<=summary.x+1);
    assert.equal(monitor.height,summary.height);
    assert.equal(route.y,graphs.y);
    assert.equal(route.height,graphs.height);
    assert.ok(route.right<=graphs.x+1,"route and graphs sit side by side");
    assert.ok(Math.abs(graphs.bottom-(h-12))<=1,"bottom panes fill the viewport");
    assert.equal(scroll,h,"desktop scroll stays inside panes");
    assert.ok(await page.isHidden("#pane-traceroute"),"MTR wins when both route options are enabled");
    await shot(page,"panes");
  });

  await check("hovering one RTT graph moves a shared cursor across all of them", async () => {
    await page.locator(".graph-card canvas >> nth=0").scrollIntoViewIfNeeded();
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

  await check("Log is a global page independent of host selection and monitor filtering", async () => {
    await page.fill("#filter","cdn");
    await page.click("#nav-log");
    await page.waitForSelector("#log-body table.log tbody tr");
    assert.equal(await page.evaluate(()=>location.hash),"#log");
    assert.ok(await page.isHidden("#workspace"));
    assert.ok(await page.isHidden("#filter"));
    assert.equal(await page.locator("#log-body button, #log-body .link").count(),0);
    const rows=await page.$$eval("#log-body tbody tr",ns=>ns.map(n=>({at:n.querySelector("time").dateTime,host:n.querySelector(".log-host").textContent})));
    assert.ok(new Set(rows.map(r=>r.host)).size>=2);
    assert.deepEqual(rows.map(r=>r.at),rows.map(r=>r.at).sort().reverse());
    await shot(page,"log-page");
    const deep=await open({viewport:{width:1400,height:900}},browser,`${base}#log`);
    await deep.waitForSelector("#log-body tbody tr");
    assert.ok(await deep.isHidden("#workspace"));
    await deep.close();
  });

  await check("live updates preserve navigation focus and selected log text", async () => {
    await page.focus("#nav-log");
    await page.waitForTimeout(2500);
    assert.equal(await page.evaluate(()=>document.activeElement.id),"nav-log");
    const logTop=await page.$eval("#log-body .table-wrap",n=>{n.scrollTop=80;return n.scrollTop;});
    assert.ok(logTop>0,"log fixture should scroll internally");
    const picked=await page.evaluate(()=>{const r=document.createRange();r.selectNodeContents(document.querySelector("#log-body .log-message"));getSelection().removeAllRanges();getSelection().addRange(r);return getSelection().toString();});
    await page.waitForTimeout(6000);
    assert.equal(await page.evaluate(()=>getSelection().toString()),picked);
    await page.evaluate(()=>getSelection().removeAllRanges());
    await page.waitForTimeout(150);
    assert.equal(await page.$eval("#log-body .table-wrap",n=>n.scrollTop),logTop);
    await page.click("#nav-monitor");
    await page.waitForSelector("#workspace:not([hidden])");
    assert.equal(await page.textContent("#inspect-target"),"cdn.example");
    await page.fill("#filter","");
  });

  await check("enabled port checks appear within Summary", async () => {
    await page.click("tr.target-row >> nth=0");
    await page.waitForFunction(()=>document.querySelector("#inspect-target").textContent === "core-rtr1.example");
    assert.match(await page.textContent("#inspect-body"), /443\/tcp/);
    assert.equal(await page.locator('#inspect-body [role="tab"]').count(),0);
  });

  await check("HTTP checks have a global page", async () => {
    await page.click("#nav-http");
    await page.waitForSelector("#http-body table");
    assert.ok(await page.isHidden("#workspace"));
    assert.deepEqual(await page.$$eval("#http-body th",ns=>ns.map(n=>n.textContent)),["Status","URL","Code","Last","Avg","Min","Max","Up","Down"]);
    assert.match(await page.textContent("#http-body tbody"),/status\.example\/health/);
  });

  await check("HTTP horizontal scroll survives live measurements and navigation", async () => {
    await page.setViewportSize(NARROWEST);
    const left=await page.$eval("#http-body .table-wrap",n=>{n.scrollLeft=120;window.savedHTTP=n;return n.scrollLeft;});
    assert.ok(left>0);
    await page.waitForTimeout(2500);
    assert.equal(await page.$eval("#http-body .table-wrap",n=>n===window.savedHTTP),true);
    assert.equal(await page.$eval("#http-body .table-wrap",n=>n.scrollLeft),left);
    await page.click("#nav-monitor");
    await page.waitForSelector("#workspace:not([hidden])");
    await page.setViewportSize(WIDE);
  });

  await check("route options select exactly one standalone pane without a host selection", async () => {
    const response=await (await fetch(`${base}api/v1/snapshot`)).json();
    for (const [traceroute,mtr] of [[true,false],[false,true],[true,true],[false,false]]) {
      const body={...response,meta:{...response.meta,features:{...response.meta.features,traceroute,mtr}}};
      const fixture=await open({viewport:{width:1400,height:900}},browser,base,body);
      assert.equal(await fixture.isVisible("#pane-mtr"),mtr);
      assert.equal(await fixture.isVisible("#pane-traceroute"),traceroute&&!mtr);
      assert.ok(await fixture.isHidden("#pane-inspect"));
      if(traceroute&&!mtr) {
        assert.equal(await fixture.locator(".trace-target").count(),response.snapshot.targets.length);
        await shot(fixture,"traceroute-pane");
      }
      await fixture.close();
    }
  });

  await check("MTR preserves scroll, column widths and selected text during live updates", async () => {
    await page.setViewportSize(NARROWEST);
    await page.click("tr.target-row >> nth=2");
    await page.waitForSelector("#mtr tbody tr");
    // The resize re-selects columns for the narrower pane; measure once laid out.
    await page.waitForFunction(()=>[...document.querySelectorAll("#mtr thead th")].every(n=>n.getBoundingClientRect().width>0));
    const geometry=()=>page.$$eval("#mtr thead th",ns=>ns.map(n=>n.getBoundingClientRect().width));
    const before=await geometry();
    const position=await page.$eval("#mtr-table-wrap",n=>{n.scrollTop=80;return {top:n.scrollTop,height:n.clientHeight}});
    assert.ok(position.top>0, "the MTR pane scrolls internally");
    const picked=await page.evaluate(()=>{
      const range=document.createRange();range.selectNodeContents(document.querySelector("#mtr td.col-ip"));
      getSelection().removeAllRanges();getSelection().addRange(range);return getSelection().toString();
    });
    await page.waitForTimeout(2500);
    assert.equal(await page.evaluate(()=>getSelection().toString()),picked);
    await page.evaluate(()=>getSelection().removeAllRanges());
    await page.waitForTimeout(1500);
    assert.deepEqual(await page.$eval("#mtr-table-wrap",n=>({top:n.scrollTop,height:n.clientHeight})),position);
    assert.deepEqual(await geometry(),before);
    await page.setViewportSize(WIDE);
  });

  await check("Summary stays stable on live updates and global-page round trips", async () => {
    const geometry=()=>page.evaluate(()=>["pane-inspect","pane-mtr","pane-graphs"].map(id=>{const r=document.getElementById(id).getBoundingClientRect();return {x:r.x+scrollX,y:r.y+scrollY,width:r.width,height:r.height};}));
    for(const viewport of [{width:1400,height:900},NARROWEST]) {
      await page.setViewportSize(viewport);
      await page.waitForTimeout(200);
      const before=await geometry();
      const summaryScroll=await page.$eval("#inspect-body",n=>{n.scrollTop=120;return {top:n.scrollTop,content:n.scrollHeight,visible:n.clientHeight};});
      assert.ok(summaryScroll.content>summaryScroll.visible && summaryScroll.top>0,"long Summary content scrolls internally");
      await page.waitForTimeout(1500);
      assert.equal(await page.$eval("#inspect-body",n=>n.scrollTop),summaryScroll.top);
      assert.deepEqual(await geometry(),before);
      await page.click("#nav-log");
      await page.waitForSelector("#page-log:not([hidden])");
      await page.click("#nav-monitor");
      await page.waitForSelector("#workspace:not([hidden])");
      assert.deepEqual(await geometry(),before);
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    }
    await page.setViewportSize(WIDE);
  });

  await check("MTR reserves its height for short, long, empty and filtered host lists", async () => {
    const body=await (await fetch(`${base}api/v1/snapshot`)).json();
    const source=body.snapshot.targets[2];
    body.meta={...body.meta,groups:[]};
    body.snapshot.targets=Array.from({length:8},(_,i)=>({...source,id:i+1,host:`mtr-${i}.example`,
      mtr_hops:Array.from({length:30},(_,j)=>({...source.mtr_hops[0],ttl:j+1}))}));
    const many=await open({viewport:{width:1400,height:900}},browser,base,body);
    assert.ok(await many.isHidden("#pane-inspect"));
    assert.equal(await many.locator("#mtr tbody").count(),8);
    const geometry=()=>many.$eval("#pane-mtr",n=>({height:n.getBoundingClientRect().height,
      scroll:n.querySelector(".table-wrap").scrollHeight,visible:n.querySelector(".table-wrap").clientHeight}));
    const full=await geometry();
    assert.ok(full.height>200);assert.ok(full.scroll>full.visible);
    await many.$eval("#mtr-table-wrap", n => { n.scrollTop = 200; });
    const sticky = await many.$eval("#mtr-table-wrap", n => {
      const head = n.querySelector(".mtr-target-row th").getBoundingClientRect();
      const bounds = n.getBoundingClientRect();
      return {top:head.top-bounds.top, text:n.querySelector(".mtr-target-label").textContent};
    });
    assert.ok(Math.abs(sticky.top - 36) <= 1, "target heading stays beneath the column headings");
    assert.match(sticky.text, /Targetmtr-0.example30 hops/);
    await many.fill("#filter","mtr-0");
    await many.waitForFunction(()=>document.querySelectorAll("#mtr tbody").length===1);
    assert.equal((await geometry()).height,full.height);
    await many.fill("#filter","no-match");
    await many.waitForSelector("#mtr-empty:not([hidden])");
    assert.equal((await geometry()).height,full.height);
    await many.close();
  });

  await check("Traceroute separates annotations and represents probe states without inventing hops", async () => {
    const result=await page.evaluate(async ()=>{
      const {tracePath}=await import("/js/traceroute.js");
      const target={host:"route.example",ip:"2001:db8::10",mtr_hops:[
        {ttl:1,ip:"192.0.2.1",asn:"AS64500",org:"Example Networks (Tokyo)",ptr:"router.lab.example",sent:0},
        {ttl:2,ip:"",sent:0},{ttl:3,ip:"",sent:0},
        {ttl:4,ip:"2001:db8::10",asn:"AS64501",org:"Destination Network",ptr:"destination.example",sent:0}]};
      const on=tracePath(target,{asn:true,ptr:true}),off=tracePath(target,{});
      const states=[{},{mtr_error:"<script>alert(1)</script>"}].map(state=>{
        const path=tracePath({...target,mtr_hops:[],...state},{});
        return {hops:path.querySelectorAll(".trace-hop").length,text:path.textContent,scripts:path.querySelectorAll("script").length};
      });
      return {on:on.textContent,off:off.textContent,addresses:[...on.querySelectorAll(".trace-address")].map(n=>n.textContent),
        unanswered:on.querySelectorAll(".is-unanswered").length,destination:on.querySelectorAll(".is-destination").length,states};
    });
    assert.match(result.on,/AS64500 Example Networks \(Tokyo\)/);
    assert.match(result.on,/router.lab.example/);
    assert.ok(!/AS64500|router.lab.example/.test(result.off));
    assert.deepEqual(result.addresses,["192.0.2.1","No reply","No reply","2001:db8::10"]);
    assert.equal(result.unanswered,2);assert.equal(result.destination,1);
    assert.ok(result.states.every(s=>s.hops===0&&s.scripts===0));
    assert.match(result.states[0].text,/Discovering/);
    assert.match(result.states[1].text,/Route discovery failed/);
  });

  await check("Traceroute stays readable at the narrowest width and disabled MTR stays hidden with cached hops", async () => {
    const body=await (await fetch(`${base}api/v1/snapshot`)).json();
    const target={...body.snapshot.targets[2],host:"route.example",ip:"2001:db8::10",
      mtr_hops:[{ttl:1,ip:"192.0.2.1",asn:"AS64500",org:"Example Networks (Tokyo)",ptr:"router.lab.example",sent:0},{ttl:2,ip:"",sent:0},
        {ttl:3,ip:"2001:db8:ffff:ffff:ffff:ffff:192.0.2.10",sent:0},{ttl:4,ip:"2001:db8::10",asn:"AS64501",org:"Destination Network",ptr:"destination.example",sent:0}]};
    body.snapshot.targets=[target];body.meta={...body.meta,groups:[],features:{traceroute:true,mtr:false,asn:true,ptr:true}};
    const route=await open({viewport:{width:1400,height:900}},browser,`${base}#target-${target.id}`,body);
    await route.waitForSelector("#pane-traceroute:not([hidden])");
    assert.ok(await route.isHidden("#pane-mtr"));
    assert.equal(await route.locator("#traceroute-scroll .trace-hop").count(),4);
    await shot(route,"traceroute");
    await route.setViewportSize(NARROWEST);
    await route.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    assert.equal(await route.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    assert.ok(await route.$eval("#traceroute-scroll",n=>n.scrollWidth<=n.clientWidth+1));
    await shot(route,"traceroute-narrow");
    await route.close();
  });

  await check("Summary and route annotations follow enabled options", async () => {
    const result = await page.evaluate(async () => {
      const {createInspect}=await import("/js/inspect.js");
      const {renderTargets}=await import("/js/table.js");
      const {renderMTR}=await import("/js/mtr.js");
      const response=await (await fetch("/api/v1/snapshot")).json();
      const source=response.snapshot.targets[0];
      const target={...source,ptr:"cached.ptr.example",asn:"AS64500",org:"Cached org",pmtu:1400,
        mtr_hops:[{ttl:1,ip:"192.0.2.1",asn:"AS64500",org:"Cached org",loss_pct:0,sent:1,last_rtt_ms:1,avg_rtt_ms:1,min_rtt_ms:1,max_rtt_ms:1,jitter_ms:0}]};
      const tabs=document.createElement("div"),body=document.createElement("div"),label=document.createElement("div");
      const inspect=createInspect({tabs,body,target:label,onSelect(){},actionsFor(){return null},canControl(){return false},requestRender(){}});
      const view={...response,snapshot:{...response.snapshot,targets:[target]},meta:{...response.meta,features:{mtr:true,asn:false,ptr:false,mtu:false,port:false,http:false}},selectedId:null};
      inspect.render(view);
      const initialTabs=[...tabs.querySelectorAll("button")].map(n=>n.dataset.tab);
      view.selectedId=target.id;
      inspect.render(view);
      const off=[...body.querySelectorAll("dt")].map(n=>n.textContent);
      view.meta.features={...view.meta.features,asn:true,ptr:true,mtu:true};
      inspect.render(view);
      const on=[...body.querySelectorAll("dt")].map(n=>n.textContent);
      const times=[...body.querySelectorAll("dt")].filter(n=>["Last probe","Last reply","Health since"].includes(n.textContent)).map(n=>n.nextElementSibling.textContent);
      const mtr=document.createElement("table");
      mtr.className="grid";mtr.append(document.createElement("thead"),document.createElement("tbody"));
      const mtrWrap=document.createElement("div");mtrWrap.append(mtr);document.body.append(mtrWrap);
      renderMTR(mtr,[target],view.meta);
      const asOn=[...mtr.querySelectorAll("th")].some(n=>n.textContent==="AS");
      view.meta.features={...view.meta.features,asn:false,ptr:false};
      renderMTR(mtr,[target],view.meta);
      const asOff=[...mtr.querySelectorAll("th")].some(n=>n.textContent==="AS");
      mtrWrap.remove();
      const wrap=document.createElement("div"),table=document.createElement("table");
      table.className="grid";table.append(document.createElement("thead"),document.createElement("tbody"));wrap.append(table);document.body.append(wrap);
      renderTargets(table,{...view,filter:"",selectedId:null},()=>{});
      const hiddenPTR=table.querySelector(".sub")==null;
      wrap.remove();
      return {initialTabs,off,on,asOn,asOff,times,hiddenPTR};
    });
    assert.deepEqual(result.initialTabs,[]);
    for (const field of ["AS","PTR","Path MTU"]) {
      assert.ok(!result.off.includes(field), `${field} shown with option off`);
      assert.ok(result.on.includes(field), `${field} missing with option on`);
    }
    assert.equal(result.asOn,true);
    assert.equal(result.asOff,false);
    assert.equal(result.hiddenPTR,true);
    assert.ok(result.times.length>0 && result.times.every(t=>/^\d{2}:\d{2}:\d{2}$/.test(t)),`times: ${result.times}`);
  });

  await check("closing details restores the overview width", async () => {
    await page.click("#close-inspect");
    await page.waitForFunction(() => document.querySelector("#pane-inspect").hidden && !document.querySelector("#workspace").classList.contains("has-inspect"));
    assert.ok(await page.isVisible("#pane-mtr"));
    assert.equal(await page.locator("#table-zoom").count(),0);
    assert.equal(await page.$eval("#workspace",n=>n.classList.contains("has-inspect")),false);
    assert.equal(await page.evaluate(()=>location.hash),"");
    assert.ok(await page.isVisible("#targets th.col-asn"));
    assert.equal(await page.locator("#targets th.col-avg, #targets th.col-spark").count(),0);
    assert.ok((await page.locator(".graph-value").allTextContents()).every(text=>!text));
    assert.match(await page.textContent("#updated"), /^Last probe \d{2}:\d{2}:\d{2}$/);
  });

  await check("filter narrows the table", async () => {
    const widths = () => page.$$eval("#targets th", (ns) => ns.map((n) => n.getBoundingClientRect().width));
    const before = await widths();
    await page.fill("#filter", "flaky");
    await page.waitForTimeout(300);
    assert.equal(await page.$$eval("tr.target-row", (r) => r.length), 1);
    assert.deepEqual(await widths(), before, "removing the scrollbar must not resize the columns");
    assert.equal(await page.locator("#mtr tbody").count(),1);
    assert.match(await page.locator("#mtr .mtr-target-row").textContent(),/flaky.example/);
    assert.ok((await page.locator("#pane-mtr").boundingBox()).height>200);
    await page.fill("#filter", "");
  });
  await page.close();

  await check("a link to a target that no longer exists is dropped from the address", async () => {
    const linked = await open({ viewport: { width: 1400, height: 900 } }, browser, `${base}#target-999999`);
    await linked.waitForFunction(() => location.hash === "");
    assert.equal(await linked.$$eval('tr.target-row[aria-current="true"]', (r) => r.length), 0);
    await linked.close();
  });

  await check("a link to an existing target selects it", async () => {
    const id = await (await fetch(`${base}api/v1/snapshot`)).json().then((b) => b.snapshot.targets[0].id);
    const linked = await open({ viewport: { width: 1400, height: 900 } }, browser, `${base}#target-${id}`);
    await linked.waitForSelector(`tr.target-row[aria-current="true"][data-target-id="${id}"]`);
    assert.equal(await linked.evaluate(() => location.hash), `#target-${id}`);
    await linked.close();
  });

  await check("without a token the dashboard is read-only", async () => {
    const ro = await open({ viewport: { width: 1400, height: 900 } });
    assert.ok(await ro.isVisible("#open-auth"));
    assert.ok(await ro.isHidden("#add-form"));
    assert.ok(await ro.isHidden("#reset-slot"));
    assert.ok(await ro.isHidden("#open-report"));
    await ro.click("tr.target-row >> nth=0");
    await ro.waitForFunction(() => !!document.querySelector("#inspect-target").textContent);
    assert.equal(await ro.$$eval('#inspect-body [data-section="actions"]', (n) => n.length), 0);
    await ro.click("#open-auth");
    assert.ok(await ro.isVisible("#readonly-explanation"));
    assert.match(await ro.textContent("#readonly-explanation"), /Press b.*Copy URL/i);
    await ro.close();
  });

  await check("a single IP target fills its pane and omits the duplicate IP column", async () => {
    const body = await (await fetch(`${base}api/v1/snapshot`)).json();
    body.meta = {...body.meta, groups: [], features: {}, measurement_state: "running"};
    body.snapshot.targets = [{...body.snapshot.targets[0], host: "8.8.8.8", ip: "8.8.8.8", ptr: "", jitter_ms: 60.4, last_rtt_ms: 6.23,
      levels: {...body.snapshot.targets[0].levels, last_rtt: "ok", jitter: "crit"},
      health: {...body.snapshot.targets[0].health, configured: true, status: "crit", reason: "Jitter 60.4 ms > 50 ms"}}];
    const single = await open({viewport: {width: 2400, height: 1000}}, browser, base, body);
    assert.equal(await single.locator("#targets .col-ip").count(), 0);
    assert.equal(await single.locator("#targets th.col-last").textContent(), "RTT");
    assert.match(await single.locator("#targets .status").textContent(), /Critical/);
    const geometry = await single.evaluate(() => {
      const pane = document.querySelector("#pane-monitor").getBoundingClientRect();
      const table = document.querySelector("#targets").getBoundingClientRect();
      const wrap = document.querySelector("#targets").parentElement;
      const cell = document.querySelector("#targets td.col-jitter");
      return {height: pane.height, gap: document.querySelector("#pane-graphs").getBoundingClientRect().top - pane.bottom,
        width: table.width, available: wrap.clientWidth, cell: getComputedStyle(cell).backgroundColor,
        value: getComputedStyle(cell.querySelector(".metric-value")).backgroundColor};
    });
    assert.ok(geometry.height > 200, `single-host panel fills its grid cell: ${geometry.height}px`);
    assert.ok(geometry.gap <= 17, `graph gap is ${geometry.gap}px`);
    assert.ok(Math.abs(geometry.width - geometry.available) <= 1, "no blank table area on the right");
    assert.equal(geometry.cell, "rgba(0, 0, 0, 0)");
    assert.notEqual(geometry.value, "rgba(0, 0, 0, 0)");
    await shot(single, "single-host");
    // Opening Summary preserves the row height for the monitor.
    await single.locator("tr.target-row").press("Enter");
    await single.waitForSelector("#pane-inspect:not([hidden])");
    assert.equal((await single.locator("#pane-monitor").boundingBox()).height,geometry.height);
    await single.close();
  });

  await check("long target lists scroll inside a bounded monitor", async () => {
    const body = await (await fetch(`${base}api/v1/snapshot`)).json();
    const source = body.snapshot.targets[0];
    body.meta = {...body.meta, groups: []};
    body.snapshot.targets = Array.from({length: 40}, (_, i) => ({...source, id: i + 1, host: `host-${i}.example`}));
    const many = await open({viewport: {width: 1400, height: 900}}, browser, base, body);
    const geometry = await many.$eval("#pane-monitor", n => ({height: n.getBoundingClientRect().height,
      scroll: n.querySelector(".pane-scroll").scrollHeight, visible: n.querySelector(".pane-scroll").clientHeight}));
    assert.ok(geometry.height <= 542);
    assert.ok(geometry.scroll > geometry.visible, "long list has an internal scrollbar");
    assert.equal(await many.locator("tr.target-row").count(), 40);
    await many.close();
  });

  await check("IP-column visibility follows configured identities and stays stable during filtering and DNS updates", async () => {
    const probe = await open({viewport: {width: 1400, height: 900}});
    const results = await probe.evaluate(async () => {
      const {targetColumns, renderTargets} = await import("/js/table.js");
      const hasIP = targets => targetColumns({}, targets).some(c => c.key === "ip");
      const response = await (await fetch("/api/v1/snapshot")).json();
      const source = response.snapshot.targets[0];
      const wrap = document.createElement("div"), table = document.createElement("table");
      table.className = "grid";
      table.append(document.createElement("thead"), document.createElement("tbody"));
      wrap.append(table); document.body.append(wrap);
      const targets = [{...source, host: "8.8.8.8", ip: "8.8.8.8"}, {...source, id: 999, host: "dns.example", ip: ""}];
      const view = {...response, snapshot: {...response.snapshot, targets}, filter: "8.8.8.8", selectedId: null};
      renderTargets(table, view, () => {});
      const beforeDNS = !!table.querySelector("th.col-ip");
      targets[1].ip = "192.0.2.9";
      renderTargets(table, view, () => {});
      const afterDNS = !!table.querySelector("th.col-ip");
      wrap.remove();
      return [hasIP([{host: "8.8.8.8"}]), hasIP([{host: "2001:db8::1"}, {host: "fe80::1%en0"}]), hasIP(targets), beforeDNS, afterDNS];
    });
    await probe.close();
    assert.deepEqual(results, [false, false, true, true, true]);
  });

  const token = process.env.E2E_TOKEN;
  assert.ok(token, "set E2E_TOKEN to the simulator's MPING_WEB_DEV_TOKEN");
  const ctx = await browser.newContext();
  const ctl = await open({ viewport: WIDE }, ctx, `${base}#token=${token}`);
  const rowHosts = () => ctl.$$eval("tr.target-row .host", (h) => h.map((x) => x.textContent));

  await check("the token link enables controls and leaves the address bar", async () => {
    await ctl.waitForSelector("#measurement-controls:not([hidden])");
    assert.equal(await ctl.evaluate(() => location.hash), "");
    assert.ok(await ctl.isDisabled("#open-auth"));
    await shot(ctl, "control");
  });

  await check("stop, report download and restart preserve counters", async () => {
    await ctl.click("#measurement-action");
    await ctl.waitForFunction(()=>document.querySelector("#measurement-action").textContent==="Restart all");
    const sent=()=>ctl.$eval("tr.target-row td.col-sent",td=>td.title);
    const before=await sent();
    await ctl.waitForTimeout(2500);
    assert.equal(await sent(),before);
    assert.equal(await ctl.locator("#report-json, #report-text").count(),0);
    await ctl.click("#open-report");
    await ctl.waitForSelector("#report-dialog[open]");
    await ctl.selectOption("#report-format","json");
    await ctl.fill("#report-name","network-check");
    const downloadPromise=ctl.waitForEvent("download");
    await ctl.click("#download-report");
    const download=await downloadPromise;
    assert.equal(download.suggestedFilename(),"network-check.json");
    const stream=await download.createReadStream();let contents="";
    for await (const chunk of stream) contents+=chunk;
    assert.equal(JSON.parse(contents).schema_version,1);
    await ctl.waitForSelector("#report-dialog",{state:"hidden"});
    await ctl.click("#measurement-action");
    await ctl.waitForFunction(()=>document.querySelector("#measurement-action").textContent==="Stop all");
    await ctl.waitForTimeout(1500);
    assert.notEqual(await sent(),before);
  });

  await check("Report chooses a format and editable name without moving the workspace", async () => {
    const geometry=()=>ctl.$eval("#workspace",n=>{const r=n.getBoundingClientRect();return {x:r.x,y:r.y,width:r.width,height:r.height};});
    const before=await geometry();
    await ctl.click("#open-report");
    await ctl.waitForSelector("#report-dialog[open]");
    await ctl.fill("#report-name","東京-回線チェック.json");
    await ctl.selectOption("#report-format","text");
    assert.equal(await ctl.inputValue("#report-name"),"東京-回線チェック.txt");
    await ctl.waitForTimeout(1500);
    assert.equal(await ctl.inputValue("#report-name"),"東京-回線チェック.txt","live snapshots preserve input");
    assert.deepEqual(await geometry(),before);
    await ctl.fill("#report-name","");
    await ctl.click("#download-report");
    assert.match(await ctl.textContent("#report-error"),/Enter a file name/);
    await ctl.fill("#report-name","folder/report");
    await ctl.click("#download-report");
    assert.match(await ctl.textContent("#report-error"),/without folders/);
    await ctl.fill("#report-name","東京-回線チェック");
    const promise=ctl.waitForEvent("download");
    await ctl.click("#download-report");
    const download=await promise;
    assert.equal(download.suggestedFilename(),"東京-回線チェック.txt");
    const stream=await download.createReadStream();let text="";
    for await(const chunk of stream)text+=chunk;
    assert.match(text,/mping/i);
    await ctl.waitForSelector("#report-dialog",{state:"hidden"});
    await ctl.click("#open-report");
    await ctl.waitForSelector("#report-dialog[open]");
    await ctl.keyboard.press("Escape");
    await ctl.waitForSelector("#report-dialog",{state:"hidden"});
    assert.equal(await ctl.evaluate(()=>document.activeElement.id),"open-report");
    await ctl.setViewportSize(NARROWEST);
    await ctl.click("#open-report");
    await ctl.waitForSelector("#report-dialog[open]");
    assert.ok(await ctl.$eval("#report-dialog",n=>{const r=n.getBoundingClientRect();return r.left>=0&&r.right<=innerWidth&&n.scrollWidth<=n.clientWidth;}));
    await shot(ctl,"report-dialog-narrow");
    await ctl.click("#cancel-report");
    await ctl.waitForSelector("#report-dialog",{state:"hidden"});
    await ctl.setViewportSize(WIDE);
  });

  await check("Report prevents duplicate downloads and preserves choices after an error", async () => {
    let requests=0;
    const fail=async route=>{
      requests++;
      await new Promise(resolve=>setTimeout(resolve,300));
      await route.fulfill({status:500,contentType:"application/json",body:JSON.stringify({error:"Report is temporarily unavailable."})});
    };
    await ctl.route("**/api/v1/report?*",fail);
    await ctl.click("#open-report");
    await ctl.waitForSelector("#report-dialog[open]");
    await ctl.selectOption("#report-format","json");
    await ctl.fill("#report-name","retry-report");
    await ctl.$eval("#report-form",form=>{form.requestSubmit();form.requestSubmit();});
    await ctl.waitForFunction(()=>document.querySelector("#report-error").textContent.includes("temporarily unavailable"));
    assert.equal(requests,1);
    assert.equal(await ctl.inputValue("#report-name"),"retry-report.json");
    assert.ok(await ctl.isVisible("#report-dialog"));
    assert.equal(await ctl.isEnabled("#download-report"),true);
    await ctl.unroute("**/api/v1/report?*",fail);
    const next=ctl.waitForEvent("download");
    await ctl.click("#download-report");
    assert.equal((await next).suggestedFilename(),"retry-report.json");
    await ctl.waitForSelector("#report-dialog",{state:"hidden"});
  });

  await check("cancelling a pending Report request does not start a download", async () => {
    let started;
    const requestStarted=new Promise(resolve=>{started=resolve;});
    const delayed=async route=>{
      started();
      await new Promise(resolve=>setTimeout(resolve,400));
      // The browser may already have aborted the intercepted request.
      try { await route.fulfill({contentType:"application/json",body:"{}"}); } catch {}
    };
    let downloads=0;
    const received=()=>downloads++;
    ctl.on("download",received);
    await ctl.route("**/api/v1/report?*",delayed);
    await ctl.click("#open-report");
    await ctl.waitForSelector("#report-dialog[open]");
    await ctl.click("#download-report");
    await requestStarted;
    await ctl.click("#cancel-report");
    await ctl.waitForSelector("#report-dialog",{state:"hidden"});
    await ctl.waitForTimeout(600);
    assert.equal(downloads,0);
    await ctl.unroute("**/api/v1/report?*",delayed);
    ctl.off("download",received);
  });

  await check("adding a host: client-side validation, then a live row", async () => {
    await ctl.click("#open-add");
    await ctl.fill("#add-host", "bad host");
    await ctl.click("#add-submit");
    assert.match(await ctl.textContent("#add-error"), /spaces/);
    await ctl.fill("#add-host", "added.e2e.example");
    await ctl.click("#add-submit");
    await ctl.waitForFunction(() => [...document.querySelectorAll(".host")].some((h) => h.textContent === "added.e2e.example"), null, { timeout: 5000 });
    assert.match(await ctl.textContent("#notice"), /Added added\.e2e\.example/);
    assert.equal(await ctl.inputValue("#add-host"), "");
  });

  await check("a double submit adds once without a spurious error", async () => {
    await ctl.click("#open-add");
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
    await ctl.click("#open-add");
    await ctl.fill("#add-host", "added.e2e.example");
    await ctl.click("#add-submit");
    await ctl.waitForSelector("#add-error:not([hidden])");
    assert.match(await ctl.textContent("#add-error"), /already in the list/);
    await ctl.locator('[data-close="add-dialog"]').click();
  });

  await check("delete confirmation survives live updates without expiring", async () => {
    await ctl.locator("tr.target-row", { hasText: "added.e2e.example" }).click();
    await ctl.getByRole("button", { name: "Delete added.e2e.example" }).click();
    assert.equal(await ctl.evaluate(()=>document.activeElement.id),"confirm-cancel");
    assert.match(await ctl.textContent("#confirm-description"),/added.e2e.example[\s\S]*Target #/);
    await ctl.waitForTimeout(6500);
    assert.ok(await ctl.isVisible("#confirm-dialog"));
    await ctl.click("#confirm-cancel");
  });

  await check("deleting needs a confirming second press", async () => {
    const row = ctl.locator("tr.target-row", { hasText: "added.e2e.example" });
    await row.click();
    const del = ctl.getByRole("button", { name: "Delete added.e2e.example" });
    await del.click();
    assert.ok((await rowHosts()).includes("added.e2e.example"), "first press must not delete");
    await ctl.locator("#confirm-action").click();
    await ctl.waitForFunction(() => ![...document.querySelectorAll(".host")].some((h) => h.textContent === "added.e2e.example"), null, { timeout: 5000 });
    assert.ok(await ctl.isHidden("#pane-inspect"), "details close when the selected target is removed");
    assert.equal(await ctl.evaluate(() => location.hash), "", "the deleted target's address is dropped");
  });

  await check("deleting a target does not restrict the global log", async () => {
    await ctl.locator("tr.target-row",{hasText:"double.e2e.example"}).click();
    await ctl.getByRole("button",{name:"Delete double.e2e.example"}).click();
    await ctl.locator("#confirm-action").click();
    await ctl.waitForFunction(()=>![...document.querySelectorAll(".host")].some(n=>n.textContent==="double.e2e.example"));
    await ctl.click("#nav-log");
    await ctl.waitForSelector("#log-body tbody tr");
    assert.ok(await ctl.isHidden("#workspace"));
    assert.ok(await ctl.isVisible("#refresh-log"));
    assert.equal(await ctl.locator("#log-body button").count(),0);
    await ctl.click("#nav-monitor");
    await ctl.waitForSelector("#workspace:not([hidden])");
  });

  await check("reset clears counters after confirmation", async () => {
    const sent = () => ctl.$eval("tr.target-row td.col-sent", (td) => Number(td.title.replaceAll(",", "")));
    const before = await sent();
    await ctl.getByRole("button", { name: "Reset stats" }).click();
    await ctl.locator("#confirm-action").click();
    await ctl.waitForFunction((b) => Number(document.querySelector("tr.target-row td.col-sent").title.replaceAll(",", "")) < b, before, { timeout: 5000 });
    assert.match(await ctl.textContent("#notice"), /Statistics reset/);
  });

  await check("a token rejected mid-session retracts every control", async () => {
    const other = await browser.newContext();
    const page2 = await open({ viewport: { width: 1400, height: 900 } }, other, `${base}#token=${token}`);
    await page2.waitForSelector("#measurement-controls:not([hidden])");
    await page2.route("**/api/v1/targets/*", (route) =>
      route.request().method() === "DELETE"
        ? route.fulfill({ status: 403, contentType: "application/json", body: '{"error":"control token missing or invalid"}' })
        : route.continue());
    await page2.click("tr.target-row >> nth=0");
    await page2.getByRole("button", { name: /^Delete / }).click();
    await page2.locator("#confirm-action").click();
    await page2.waitForFunction(() => document.querySelector("#measurement-controls").hidden);
    assert.ok(await page2.isVisible("#open-auth"));
    assert.equal(await page2.$$eval('#inspect-body [data-section="actions"]', (n) => n.length), 0);
    await other.close();
  });

  await check("opening via localhost provides in-place token authentication", async () => {
    const lh = await browser.newContext();
    const page3 = await open({ viewport: { width: 1400, height: 900 } }, lh, base.replace("127.0.0.1", "localhost"));
    assert.ok(await page3.isVisible("#open-auth"));
    await page3.click("#open-auth");
    await page3.fill("#auth-token", `${base}#token=${token}`);
    await page3.click("#auth-submit");
    await page3.waitForSelector("#auth-dialog",{state:"hidden"});
    assert.ok(await page3.isDisabled("#open-auth"));
    await lh.close();
  });

  await check("a stale token falls back to read-only", async () => {
    const stale = await browser.newContext();
    const page = await open({ viewport: { width: 1400, height: 900 } }, stale, `${base}#token=not-the-token`);
    await page.waitForTimeout(500);
    assert.ok(await page.isVisible("#open-auth"));
    assert.ok(await page.isHidden("#add-form"));
    await stale.close();
  });

  const narrow = await open({ viewport: NARROWEST }, ctx);
  await check("the narrowest width keeps status, host and the highest-priority columns without page overflow", async () => {
    assert.ok(await narrow.isVisible("#open-report"));
    assert.equal(await narrow.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
    const headers = await narrow.$$eval("#targets th", (h) => h.map((x) => x.textContent));
    assert.deepEqual(headers.slice(0, 2), ["Status", "Host"]);
    for (const kept of ["Loss %", "RTT"]) assert.ok(headers.includes(kept), `${kept} must stay: ${headers}`);
    assert.ok(await narrow.$eval("#targets", (n) => n.getBoundingClientRect().width <= n.parentElement.clientWidth + 1));
    await shot(narrow, "narrowest");
  });

  await check("MTR shows all hosts in the stacked layout and stays open when selection is cleared", async () => {
    assert.ok(await narrow.isHidden("#pane-inspect"));
    assert.ok(await narrow.isVisible("#pane-mtr"));
    const hosts=()=>narrow.$$eval("#mtr .mtr-target-row",ns=>ns.map(n=>n.textContent));
    const before=await hosts();
    assert.equal(before.length,6);
    assert.equal((await narrow.locator("#pane-mtr").boundingBox()).height,420);
    const position=await narrow.$eval("#mtr-table-wrap",n=>{n.scrollTop=100;return n.scrollTop});
    assert.ok(position>0);
    await narrow.locator("tr.target-row",{hasText:"cdn.example"}).click();
    await narrow.waitForSelector("#pane-inspect:not([hidden])");
    assert.deepEqual(await hosts(),before);
    assert.equal(await narrow.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    await narrow.locator("#close-inspect").click();
    await narrow.waitForSelector("#pane-inspect", {state:"hidden"});
    assert.ok(await narrow.isVisible("#pane-mtr"));
    assert.deepEqual(await hosts(),before);
    assert.equal(await narrow.$eval("#mtr-table-wrap",n=>n.scrollTop),position);
    await shot(narrow,"mtr-stacked");
  });

  await check("a window narrower than supported scrolls the page instead of compressing it", async () => {
    await narrow.setViewportSize({width:390,height:844});
    await narrow.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
    assert.ok(await narrow.evaluate(()=>document.documentElement.scrollWidth>=720));
    assert.ok(await narrow.$eval("#workspace",n=>n.getBoundingClientRect().width>=720-1));
  });

  await check("no console errors", async () => assert.deepEqual(problems, []));
} finally {
  await browser.close();
}
