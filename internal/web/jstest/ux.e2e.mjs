// Interaction regression checks. Run against TestDevServer like e2e.mjs.
// Only SSE/history are deterministic fixtures; authentication uses the real server.
import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.PLAYWRIGHT_CORE??'playwright-core');
const base=process.argv[2]??'http://127.0.0.1:18091/';
const token=process.env.E2E_TOKEN;
assert.ok(token,'set E2E_TOKEN');
const browser=await chromium.launch(process.env.CHROMIUM_PATH?{executablePath:process.env.CHROMIUM_PATH}:{});
const failures=[];
const source=await(await fetch(`${base}api/v1/snapshot`)).json();
const fixture=structuredClone(source);
fixture.snapshot.targets=fixture.snapshot.targets.slice(0,6);
fixture.meta.interval_ms=1000;
fixture.snapshot.targets.forEach((t,i)=>{
  t.health={...t.health,configured:true,status:['ok','warn','crit','pending','ok','ok'][i],last_probe_at:'2026-10-16T08:30:00Z'};
  t.last_rtt_ms=(i+1)*10;
});
const settle=p=>p.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
async function open(body=fixture,width=1440){
 const p=await browser.newPage({viewport:{width,height:1000},timezoneId:'Asia/Tokyo'});
 p.on('pageerror',e=>failures.push(e.message));
 await p.addInitScript(body=>{
  window.EventSource=class extends EventTarget{
   constructor(){super();window.testStream=this;setTimeout(()=>{this.dispatchEvent(new Event('open'));this.emit(body);},100);}
   emit(body){this.dispatchEvent(new MessageEvent('snapshot',{data:JSON.stringify(body)}));}
   close(){}
  };
 },body);
 await p.route('**/api/v1/history?*',r=>r.fulfill({contentType:'application/json',body:JSON.stringify({captured_at:'2026-10-16T08:30:00Z',targets:body.snapshot.targets.map(t=>({id:t.id,rtt_ms:[10,null,30]}))})}));
 await p.goto(base);await p.waitForSelector('#shown-count:text("hosts")');await settle(p);return p;
}
const check=async(name,fn)=>{await fn();console.log(`ok - ${name}`);};
const ids=p=>p.locator('#targets .target-row').evaluateAll(ns=>ns.map(n=>Number(n.dataset.targetId)));
const emit=async(p,body)=>{await p.evaluate(b=>window.testStream.emit(b),body);await settle(p);};
const geometry=p=>p.locator('#workspace').evaluate(n=>{const r=n.getBoundingClientRect();return [r.x,r.y,r.width,r.height];});
try{
 const p=await open();
 await check('health chips combine with search and preserve a filtered-out Summary',async()=>{
  await p.locator('.host-open').first().click();await settle(p);
  await p.click('#summary [data-status=crit]');await settle(p);
  assert.equal(await p.locator('#targets .target-row').count(),1);
  assert.equal(await p.locator('#mtr tbody').count(),1);
  assert.equal(await p.locator('.graph-card').count(),1);
  assert.equal(await p.textContent('#shown-count'),'1 / 6 hosts');
  assert.ok(await p.isVisible('#selection-note'));assert.ok(await p.isVisible('#pane-inspect'));
  await p.fill('#filter','no-such-host');await settle(p);
  assert.ok(await p.isVisible('#targets-empty'));assert.ok(await p.isVisible('#graphs-empty'));
  await p.click('#empty-action');await settle(p);assert.equal((await ids(p)).length,6);
  assert.ok(await p.isHidden('#selection-note'));await p.click('#close-inspect');
 });
 await check('global sort stays frozen during incoming measurements until explicitly refreshed',async()=>{
  await p.selectOption('#sort-order','last:desc');await settle(p);
  const before=await ids(p);assert.deepEqual(before,[6,5,4,3,2,1]);assert.equal(await p.locator('#targets .group-row').count(),0);
  assert.match(await p.locator('#targets .sub').allTextContents().then(x=>x.join(' ')),/core|internet/);
  const next=structuredClone(fixture);next.snapshot.targets[0].last_rtt_ms=900;
  await emit(p,next);assert.deepEqual(await ids(p),before);assert.ok(await p.isEnabled('#refresh-sort'));
  await p.click('#refresh-sort');await settle(p);assert.equal((await ids(p))[0],1);
  await p.selectOption('#sort-order','config:asc');await settle(p);assert.deepEqual(await ids(p),[1,2,3,4,5,6]);
  await p.locator('#targets [data-sort=status]').focus();await p.keyboard.press('Enter');await settle(p);
  assert.equal((await ids(p))[0],3);assert.equal(await p.getAttribute('#targets th.col-status','aria-sort'),'descending');
  await emit(p,fixture);assert.equal(await p.evaluate(()=>document.activeElement.dataset.sort),'status');
  await p.selectOption('#sort-order','config:asc');await settle(p);
 });
 await check('roving host focus and shortcuts respect inputs, composition and preferences',async()=>{
  assert.equal(await p.locator('.host-open[tabindex="0"]').count(),1);
  await p.locator('.host-open').first().focus();await p.keyboard.press('j');
  assert.equal(await p.evaluate(()=>document.activeElement.dataset.targetId),'2');assert.ok(await p.isHidden('#pane-inspect'));
  await p.keyboard.press('Enter');await settle(p);assert.equal(await p.textContent('#inspect-target'),fixture.snapshot.targets[1].host);
  await p.keyboard.press('/');await settle(p);assert.equal(await p.evaluate(()=>document.activeElement.id),'filter');
  await p.keyboard.type('jk/w?');assert.equal(await p.inputValue('#filter'),'jk/w?');
  await p.fill('#filter','');await settle(p);await p.keyboard.press('Escape');assert.equal(await p.evaluate(()=>document.activeElement.dataset.targetId),'2');
  await p.locator('.host-open').nth(1).dispatchEvent('keydown',{key:'j',isComposing:true});
  assert.equal(await p.evaluate(()=>document.activeElement.dataset.targetId),'2');
  await p.keyboard.press('?');await p.waitForSelector('#shortcuts-dialog[open]');await p.uncheck('#shortcuts-enabled');await p.keyboard.press('Escape');
  await p.locator('.host-open').nth(1).focus();await p.keyboard.press('j');assert.equal(await p.evaluate(()=>document.activeElement.dataset.targetId),'2');
  await p.keyboard.press('ArrowDown');assert.equal(await p.evaluate(()=>document.activeElement.dataset.targetId),'3');
  await p.click('#open-shortcuts');await p.check('#shortcuts-enabled');await p.keyboard.press('Escape');
 });
 await check('MTR selection is explicit and never auto-scrolls; locate acts on its own pane',async()=>{
  await p.locator('#mtr-table-wrap').evaluate(n=>n.scrollTop=180);
  const top=await p.locator('#mtr-table-wrap').evaluate(n=>n.scrollTop);
  await p.locator('.host-open').last().click();await settle(p);
  assert.equal(await p.locator('#mtr-table-wrap').evaluate(n=>n.scrollTop),top);
  assert.equal(await p.locator('#mtr .mtr-target-row.is-selected').count(),1);
  await emit(p,fixture);assert.equal(await p.locator('#mtr-table-wrap').evaluate(n=>n.scrollTop),top);
  await p.click('#locate-mtr');assert.ok(await p.locator('#mtr-table-wrap').evaluate(n=>n.scrollTop)>top);
 });
 await check('keyboard graph readouts distinguish measurement, loss and missing history without moving panes',async()=>{
  const chart=p.locator('.graph-card').first();await chart.focus();const before=await geometry(p);
  await p.keyboard.press('End');await settle(p);assert.match(await chart.locator('.graph-readout').textContent(),/17:30:00.*RTT 30/);
  await p.keyboard.press('ArrowLeft');await settle(p);assert.match(await chart.locator('.graph-readout').textContent(),/No reply/);
  await p.keyboard.press('Home');await settle(p);assert.match(await chart.locator('.graph-readout').textContent(),/No measurement/);
  const announcement=await p.locator('#graphs + [role=status]').textContent();await emit(p,fixture);
  assert.equal(await p.locator('#graphs + [role=status]').textContent(),announcement);
  assert.equal(await p.evaluate(()=>document.activeElement.className),'graph-card');
  await p.keyboard.press('Escape');await settle(p);assert.equal(await chart.locator('.graph-value').textContent(),'');
  assert.deepEqual(await geometry(p),before);assert.equal(await p.evaluate(()=>document.documentElement.scrollHeight),1000);
 });
 await check('token input rejects bad values without persisting and distinguishes a connection failure',async()=>{
  await p.click('#open-auth');assert.equal(await p.getAttribute('#auth-token','type'),'password');
  await p.fill('#auth-token','not-the-current-token');await p.click('#auth-submit');await p.waitForSelector('#auth-error:not([hidden])');
  assert.match(await p.textContent('#auth-error'),/not accepted/);assert.equal(await p.evaluate(()=>localStorage.getItem('mping.token')),null);
  const abort=r=>r.abort('failed');await p.route('**/api/v1/session',abort);
  await p.fill('#auth-token',token);await p.click('#auth-submit');await p.waitForFunction(()=>document.querySelector('#auth-error').textContent.includes('Cannot reach'));
  await p.unroute('**/api/v1/session',abort);await p.check('#auth-show');assert.equal(await p.getAttribute('#auth-token','type'),'text');
  await p.keyboard.press('Escape');await p.waitForFunction(()=>document.querySelector('#auth-token').value==='');
 });
 await check('pasting a control URL enables controls without reloading or losing context; notification overlays layout',async()=>{
  await p.fill('#filter','core');await settle(p);await p.locator('.host-open').first().click();await settle(p);
  const before=await geometry(p),hash=await p.evaluate(()=>location.hash);
  await p.evaluate(()=>window.noReloadMarker=42);await p.click('#open-auth');await p.fill('#auth-token',`${base}#token=${token}`);await p.click('#auth-submit');
  await p.waitForSelector('#auth-dialog',{state:'hidden'});assert.ok(await p.isDisabled('#open-auth'));
  assert.equal(await p.inputValue('#filter'),'core');assert.equal(await p.evaluate(()=>location.hash),hash);assert.equal(await p.evaluate(()=>window.noReloadMarker),42);
  assert.equal(await p.evaluate(()=>localStorage.getItem('mping.token')),token);assert.deepEqual(await geometry(p),before);
  assert.equal(await p.locator('#notice').evaluate(n=>getComputedStyle(n).position),'fixed');
  await p.click('#dismiss-notice');assert.ok(await p.isHidden('#notice'));
  await p.keyboard.press('w');await p.waitForSelector('#report-dialog[open]');await p.keyboard.press('Escape');
 });
 await check('operation errors persist until dismissal and do not resize the workspace',async()=>{
  await p.route('**/api/v1/stop',r=>r.fulfill({status:500,contentType:'application/json',body:'{"error":"Temporary stop failure."}'}));
  const before=await geometry(p);await p.click('#measurement-action');await p.waitForSelector('#notice.is-error');
  assert.equal(await p.getAttribute('#notice-message','role'),'alert');
  await p.waitForTimeout(4500);assert.ok(await p.isVisible('#notice'));assert.deepEqual(await geometry(p),before);
  await p.click('#dismiss-notice');assert.ok(await p.isHidden('#notice'));
 });
 await check('delete and reset confirmation are cancellable and identify the exact scope',async()=>{
  await p.locator('.host-open').first().focus();await p.keyboard.press('Delete');await p.waitForSelector('#confirm-dialog[open]');
  assert.equal(await p.evaluate(()=>document.activeElement.id),'confirm-cancel');assert.match(await p.textContent('#confirm-description'),/Target #1/);
  await p.keyboard.press('Escape');assert.ok(await p.isHidden('#confirm-dialog'));
  await p.getByRole('button',{name:'Reset stats',exact:true}).click();assert.match(await p.textContent('#confirm-description'),/all hosts/);
  await p.click('#confirm-cancel');
 });
 await check('mobile action menu and filters fit 320/360/390px in both themes without moving panes',async()=>{
  await p.click('#clear-filter');await settle(p);
  for(const theme of ['light','dark'])for(const width of [320,360,390]){
   await p.emulateMedia({colorScheme:theme});await p.setViewportSize({width,height:844});await settle(p);
   assert.equal(await p.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`${theme} ${width}`);
   assert.ok((await p.locator('#status-filter').boundingBox()).width>100);
   const before=await geometry(p);await p.click('#toggle-actions');assert.ok(await p.isVisible('#open-report'));assert.deepEqual(await geometry(p),before);
   await p.keyboard.press('Escape');assert.ok(await p.isHidden('#open-report'));
  }
  await p.click('#toggle-actions');await p.click('#open-add');await p.waitForSelector('#add-dialog[open]');await p.keyboard.press('Escape');
  await p.waitForFunction(()=>document.activeElement.id==='toggle-actions');
 });
 await p.close();
 await check('cancelling pending authentication never stores the candidate',async()=>{
  const q=await open();let started;const request=new Promise(r=>started=r);
  await q.route('**/api/v1/session',async r=>{started();await new Promise(resolve=>setTimeout(resolve,300));try{await r.fulfill({contentType:'application/json',body:'{"control":true}'});}catch{}});
  await q.click('#open-auth');await q.fill('#auth-token',token);await q.click('#auth-submit');await request;
  await q.locator('[data-close=auth-dialog]').click();await q.waitForTimeout(400);
  assert.equal(await q.evaluate(()=>localStorage.getItem('mping.token')),null);assert.ok(await q.isEnabled('#open-auth'));await q.close();
 });
 await check('connection loss retains timestamps and measurements; stopped is independent of online',async()=>{
  const q=await open();assert.match(await q.textContent('#updated'),/17:30:00/);
  await q.evaluate(()=>window.testStream.dispatchEvent(new Event('error')));await settle(q);
  assert.match(await q.textContent('#badge'),/Reconnecting/);assert.match(await q.textContent('#connection-note'),/last received values/);
  assert.match(await q.textContent('#updated'),/17:30:00/);assert.equal((await ids(q)).length,6);
  const stopped=structuredClone(fixture);stopped.meta.measurement_state='stopped';await emit(q,stopped);
  await q.evaluate(()=>window.testStream.dispatchEvent(new Event('open')));await settle(q);
  assert.match(await q.textContent('#badge'),/Online/);assert.match(await q.textContent('#measurement-state'),/stopped/);await q.close();
 });
 await check('zero targets and unconfigured routes explain next steps; empty graphs are explicit',async()=>{
  const empty=structuredClone(fixture);empty.snapshot.targets=[];empty.meta.features={};
  const q=await open(empty);assert.match(await q.textContent('#targets-empty-message'),/Add a host/);assert.equal(await q.textContent('#empty-action'),'Enable controls');
  assert.ok(await q.isHidden('#pane-mtr'));assert.ok(await q.isHidden('#pane-traceroute'));assert.match(await q.textContent('#monitor-hint'),/-M or -T/);
  await q.click('#empty-action');assert.ok(await q.isVisible('#auth-dialog'));await q.close();
  const one=structuredClone(fixture);one.snapshot.targets=one.snapshot.targets.slice(0,1);one.meta.features.mtr=false;one.meta.features.traceroute=true;
  const r=await open(one);await r.route('**/api/v1/history?*',route=>route.fulfill({contentType:'application/json',body:JSON.stringify({captured_at:'2026-10-16T08:30:00Z',targets:[{id:1,rtt_ms:[]}]})}));
  await r.waitForTimeout(2200);assert.ok(await r.isVisible('.graph-empty'));await r.locator('.host-open').click();await settle(r);
  assert.equal(await r.locator('.trace-target.is-selected').count(),1);assert.ok(await r.isVisible('#locate-traceroute'));await r.close();
 });
 assert.deepEqual(failures,[]);console.log('UX interaction checks passed.');
}finally{await browser.close();}
