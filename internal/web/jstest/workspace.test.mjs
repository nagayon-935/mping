import {test} from "node:test";
import assert from "node:assert/strict";
import {configuredTargets,sortTargets,frozenOrder,filterTargets} from "../static/js/workspace.js";
import {controlToken} from "../static/js/control.js";
const th={loss_warn_pct:20,loss_crit_pct:80,rtt_warn_ms:50,rtt_crit_ms:200,jitter_warn_ms:10,jitter_crit_ms:50};
const meta={thresholds:th,features:{ptr:true,asn:true}};
const target=(id,over={})=>({id,host:`host-${id}`,ip:`192.0.2.${id}`,sent:10,recv:10,loss:0,last_rtt_ms:id,health:{configured:true,status:"ok"},...over});
const ids=targets=>targets.map(t=>t.id);

test("status sorting compares all groups and keeps equal targets in configured order",()=>{
 const ts=[target(1),target(2,{health:{configured:true,status:"crit"}}),target(3,{health:{configured:true,status:"pending"}}),target(4,{health:{configured:true,status:"warn"}}),target(5)];
 const configured=configuredTargets(ts,[{name:"core",target_ids:[5,1]},{name:"external",target_ids:[2,3,4]}]);
 assert.deepEqual(ids(configured),[5,1,2,3,4]);
 assert.deepEqual(ids(sortTargets(configured,{key:"status",direction:"desc"},th)),[2,4,3,5,1]);
});
test("measurement updates do not change a frozen order until explicitly refreshed",()=>{
 const ts=[target(1,{last_rtt_ms:10}),target(2,{last_rtt_ms:100})];
 const saved=ids(sortTargets(ts,{key:"last",direction:"desc"},th));
 ts[0].last_rtt_ms=500;
 assert.deepEqual(ids(frozenOrder(ts,saved)),[2,1]);
 assert.deepEqual(ids(sortTargets(ts,{key:"last",direction:"desc"},th)),[1,2]);
 assert.deepEqual(ids(frozenOrder([ts[0],target(3)],saved)),[1,3]);
});
test("unknown RTT and loss stay last in either direction, not confused with zero loss",()=>{
 const ts=[target(1,{last_rtt_ms:null,recv:0,sent:0}),target(2,{last_rtt_ms:10}),target(3,{last_rtt_ms:100,recv:5,loss:5})];
 for(const key of ["last","loss"]){
  assert.deepEqual(ids(sortTargets(ts,{key,direction:"desc"},th)),[3,2,1]);
  assert.deepEqual(ids(sortTargets(ts,{key,direction:"asc"},th)),[2,3,1]);
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
