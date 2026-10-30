import {test} from "node:test";
import assert from "node:assert/strict";
import {configuredTargets,sortTargets,frozenOrder,filterTargets,createTargetOrder} from "../static/js/workspace.js";
import {controlToken} from "../static/js/control.js";
const meta={features:{ptr:true,asn:true}};
const target=(id,over={})=>({id,host:`host-${id}`,ip:`192.0.2.${id}`,sent:10,recv:10,loss:0,last_rtt_ms:id,health:{configured:true,status:"ok"},...over});
const ids=targets=>targets.map(t=>t.id);

test("status sorting compares all groups and keeps equal targets in configured order",()=>{
 const ts=[target(1),target(2,{health:{configured:true,status:"crit"}}),target(3,{health:{configured:true,status:"pending"}}),target(4,{health:{configured:true,status:"warn"}}),target(5)];
 const configured=configuredTargets(ts,[{name:"core",target_ids:[5,1]},{name:"external",target_ids:[2,3,4]}]);
 assert.deepEqual(ids(configured),[5,1,2,3,4]);
 assert.deepEqual(ids(sortTargets(configured,{key:"status",direction:"desc"})),[2,4,3,5,1]);
});
test("measurement updates do not change a frozen order until explicitly refreshed",()=>{
 const ts=[target(1,{last_rtt_ms:10}),target(2,{last_rtt_ms:100})];
 const saved=ids(sortTargets(ts,{key:"last",direction:"desc"}));
 ts[0].last_rtt_ms=500;
 assert.deepEqual(ids(frozenOrder(ts,saved)),[2,1]);
 assert.deepEqual(ids(sortTargets(ts,{key:"last",direction:"desc"})),[1,2]);
 assert.deepEqual(ids(frozenOrder([ts[0],target(3)],saved)),[1,3]);
});
test("unknown RTT and loss stay last in either direction, not confused with zero loss",()=>{
 const ts=[target(1,{last_rtt_ms:null,recv:0,sent:0}),target(2,{last_rtt_ms:10}),target(3,{last_rtt_ms:100,recv:5,loss:5})];
 for(const key of ["last","loss"]){
  assert.deepEqual(ids(sortTargets(ts,{key,direction:"desc"})),[3,2,1]);
  assert.deepEqual(ids(sortTargets(ts,{key,direction:"asc"})),[2,3,1]);
 }
});
test("health and text filters combine without changing their source order",()=>{
 const ts=[target(1,{host:"gateway",health:{configured:true,status:"crit"}}),target(2,{host:"gateway-backup"}),target(3,{health:{configured:true,status:"crit"}})];
 assert.deepEqual(ids(filterTargets(ts,"GATE","crit",meta)),[1]);
 assert.deepEqual(ids(filterTargets(ts,"","crit",meta)),[1,3]);
 assert.deepEqual(ids(filterTargets(ts,"missing","all",meta)),[]);
});
test("tokens accept raw values or a copied loopback URL without navigating",()=>{
 const token="current-token-for-test_123";
 assert.equal(controlToken(token),token);
 for(const host of ["127.0.0.1","localhost","[::1]"])assert.equal(controlToken(`http://${host}:8080/#token=${token}`,"http://127.0.0.1:8080"),token);
 for(const input of ["short","bad token with spaces",`http://example.com:8080/#token=${token}`,`http://127.0.0.1:8090/#token=${token}`,`https://localhost:8080/#token=${token}`])assert.equal(controlToken(input,"http://127.0.0.1:8080"),null);
});


test("prepared workspace keeps all hosts ranked when a filter hides the leaders", () => {
  const order = createTargetOrder();
  const view = { snapshot: { targets: [target(1), target(2)] }, meta,
    sort: {key: "last", direction: "desc"}, filter: "host-1", status: "all" };
  const first = order.derive(view);
  assert.deepEqual(ids(first.targets), [1]);
  assert.equal(first.total, 2);
  assert.equal(first.orderUpToDate, true);

  view.snapshot.targets[0].last_rtt_ms = 500;
  assert.equal(order.derive(view).orderUpToDate, false);
  view.filter = "";
  assert.deepEqual(ids(order.derive(view).targets), [2, 1]);
  order.refresh(view);
  assert.deepEqual(ids(order.derive(view).targets), [1, 2]);
  assert.equal(order.derive(view).orderUpToDate, true);
});

test("workspace membership changes retain frozen rows and configured order restores groups", () => {
  const order = createTargetOrder();
  const view = {snapshot: {targets: [target(1), target(2), target(3)]},
    meta: {...meta, groups: [{name: "core", target_ids: [2, 1]}]},
    sort: {key: "last", direction: "desc"}, filter: "", status: "all"};
  assert.deepEqual(ids(order.derive(view).targets), [3, 2, 1]);
  view.snapshot.targets = [target(1), target(3), target(4)];
  assert.deepEqual(ids(order.derive(view).targets), [3, 1, 4]);
  assert.equal(order.derive(view).orderUpToDate, false);
  view.sort = {key: "config", direction: "asc"};
  assert.deepEqual(ids(order.derive(view).targets), [1, 3, 4]);
  assert.equal(order.derive(view).orderUpToDate, true);
  view.snapshot.targets = [];
  assert.deepEqual(order.derive(view), {targets: [], total: 0, groupHeaders: 0, orderUpToDate: true});
});

test("group headings count what the unfiltered table draws", () => {
  const order = createTargetOrder();
  const view = {snapshot: {targets: [target(1), target(2), target(3)]},
    meta: {...meta, groups: [{name: "core", target_ids: [1]}, {name: "gone", target_ids: [9]}]},
    sort: {key: "config", direction: "asc"}, filter: "host-3", status: "all"};
  // core and Ungrouped; the empty group draws no heading and the filter is ignored.
  assert.equal(order.derive(view).groupHeaders, 2);
  view.sort = {key: "host", direction: "asc"};
  assert.equal(order.derive(view).groupHeaders, 0, "a column sort flattens the table");
  view.sort = {key: "config", direction: "asc"};
  view.meta = {...meta, groups: []};
  assert.equal(order.derive(view).groupHeaders, 0, "no groups, no headings");
});
