import { el, statusChip } from "./dom.js";
import { sameValues, syncChildren } from "./live_rows.js";
import { targetAddress } from "./model.js";

/** The discovered hops of one target (mtr_hops). AS and PTR annotations
 * follow the current feature flags. */
export function tracePath(target, features) {
  const hops = target.mtr_hops ?? [];
  const failure = target.mtr_error && el("div", { className: "trace-message" },
    statusChip("warn", "Route discovery failed"), el("p", { text: target.mtr_error }));
  if (!hops.length) {
    return failure || el("div", { className: "trace-message" }, statusChip("pending", "Discovering…"));
  }
  const noReply = hops.filter((hop) => !hop.ip).length;
  const summary = `${hops.length} ${hops.length === 1 ? "hop" : "hops"} · ${noReply} no reply`;
  const list = el("ol", { className: "trace-path", attrs: { "aria-label": "Traceroute hops" } });
  for (const hop of collapseSilentRuns(hops)) {
    if (hop.silent) {
      list.append(el("li", { className: "trace-hop is-unanswered is-run" },
        el("span", { className: "trace-number", text: "⋯", attrs: { "aria-label": `Hops ${hop.first} to ${hop.last}` } }),
        el("div", { className: "trace-identity" },
          el("span", { className: "trace-address", text: `No reply (${hop.count} hops)` }),
          el("span", { className: "trace-annotation", text: `Hops ${hop.first}–${hop.last}` }))));
      continue;
    }
    const destination = !!hop.ip && [target.ip, target.host].some((ip) => ip && hop.ip.toLowerCase() === ip.toLowerCase());
    const identity = el("div", { className: "trace-identity" },
      el("span", { className: "trace-address", text: hop.ip || "No reply" }));
    if (features.ptr && hop.ptr) identity.append(el("span", { className: "trace-annotation", text: hop.ptr }));
    const asn = [hop.asn, hop.org].filter(Boolean).join(" ");
    if (features.asn && hop.asn) identity.append(el("span", { className: "trace-annotation", text: asn }));
    list.append(el("li", { className: `trace-hop${hop.ip ? "" : " is-unanswered"}${destination ? " is-destination" : ""}` },
      el("span", { className: "trace-number", text: String(hop.ttl), attrs: { "aria-label": `Hop ${hop.ttl}` } }), identity,
      destination && el("span", { className: "trace-tag", text: "Destination" })));
  }
  return el("div", { className: "trace-result" }, failure,
    el("p", { className: "trace-summary", text: summary }), list);
}

/** Runs of SILENT_RUN or more consecutive unanswered hops become one
 * {silent, first, last, count} entry; shorter runs stay as individual hops. */
const SILENT_RUN = 3;
function collapseSilentRuns(hops) {
  const out = [];
  let run = [];
  const flush = () => {
    if (run.length >= SILENT_RUN) out.push({ silent: true, first: run[0].ttl, last: run.at(-1).ttl, count: run.length });
    else out.push(...run);
    run = [];
  };
  for (const hop of hops) {
    if (hop.ip) { flush(); out.push(hop); } else run.push(hop);
  }
  flush();
  return out;
}

function traceTarget(target, features) {
  const address = targetAddress(target);
  return el("section", {
    className: "trace-target",
    attrs: { "data-target-id": String(target.id), "aria-label": target.host },
  }, el("div", { className: "trace-target-head" },
    el("span", { className: "mtr-target-kind", text: "Target" }),
    el("strong", { className: "cell-text", text: target.host, attrs: { title: target.host } }),
    address && el("span", { className: "trace-target-address mono cell-text", text: address, attrs: { title: target.ip || "" } })),
    tracePath(target, features));
}

const renderedRoutes = new WeakMap();

/** All hosts are visible independently of the selected Summary target. */
export function renderTraceroutes(container, targets, features, selectedId = null) {
  const top = container.scrollTop;
  const previous = renderedRoutes.get(container);
  const entries = new Map();
  if (!targets.length) {
    if (!container.firstElementChild?.classList.contains("empty")) {
      container.replaceChildren(el("p", { className: "empty", text: "No targets match the filter." }));
    }
  } else {
    for (const target of targets) {
      // The same snapshot also carries per-hop statistics, but a trace only
      // shows identities and the discovery result.
      const values = [target.host, target.ip, target.mtr_error];
      for (const hop of target.mtr_hops ?? []) {
        values.push(hop.ttl, hop.ip, features.ptr && hop.ptr, features.asn && hop.asn, features.asn && hop.org);
      }
      const old = previous?.get(target.id);
      const node = sameValues(old?.values, values) ? old.node : traceTarget(target, features);
      node.classList.toggle("is-selected", target.id === selectedId);
      entries.set(target.id, { node, values });
    }
    syncChildren(container, [...entries.values()].map(({ node }) => node));
  }
  renderedRoutes.set(container, entries);
  if (container.scrollTop !== top) container.scrollTop = top;
}
