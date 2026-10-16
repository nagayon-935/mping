import {test} from "node:test";
import assert from "node:assert/strict";
import {mtrColumns} from "../static/js/mtr.js";
import {selectColumns} from "../static/js/model.js";
import {parseSort} from "../static/js/toolbar.js";

// Mirrors the CSS width contract in style.css (.grid custom properties).
const px={ttl:60,ip:196,asn:200,hopLoss:96,sent:88,recv:88,last:100,avg:100,min:100,max:100,jitter:100};
const keys=(available,features={asn:true})=>selectColumns(mtrColumns(features),c=>px[c.key],available).map(c=>c.key);

test("MTR keeps hop, address, loss, RTT and average even when they do not fit",()=>{
 assert.deepEqual(keys(300),["ttl","ip","hopLoss","last","avg"]);
});
test("MTR adds Worst and Best before AS, counts and jitter as the pane widens",()=>{
 assert.deepEqual(keys(760),["ttl","ip","hopLoss","last","avg","min","max"]);
 assert.deepEqual(keys(960),["ttl","ip","asn","hopLoss","last","avg","min","max"]);
});
test("MTR shows every column, in display order, once the pane is wide enough",()=>{
 assert.deepEqual(keys(4000),["ttl","ip","asn","hopLoss","sent","recv","last","avg","min","max","jitter"]);
});
test("MTR omits the AS column when AS lookup is disabled",()=>{
 assert.ok(!keys(4000,{asn:false}).includes("asn"));
});

test("a stored sort is restored only when its key and direction are still offered",()=>{
 assert.deepEqual(parseSort(JSON.stringify({key:"last",direction:"desc"})),{key:"last",direction:"desc"});
 assert.equal(parseSort(JSON.stringify({key:"removed",direction:"desc"})),null);
 assert.equal(parseSort(JSON.stringify({key:"last",direction:"sideways"})),null);
 assert.equal(parseSort("{not json"),null);
 assert.equal(parseSort(null),null);
});
