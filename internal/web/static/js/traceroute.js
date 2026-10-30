import { el, statusChip } from "./dom.js";
import { targetAddress } from "./model.js";

/** The discovered hops of one target (mtr_hops). AS and PTR annotations
 * follow the current feature flags. */
export function tracePath(target, features) {
  const hops = target.mtr_hops ?? [];
  if (!hops.length) {
    if (target.mtr_error) return el("div", { className: "trace-message" },
      statusChip("warn", "Route discovery failed"), el("p", { text: target.mtr_error }));
    return el("div", { className: "trace-message" }, statusChip("pending", "Discovering…"));
  }
  const noReply = hops.filter((hop) => !hop.ip).length;
  const summary = `${hops.length} ${hops.length === 1 ? "hop" : "hops"} · ${noReply} no reply`;
  const list = el("ol", { className: "trace-path", attrs: { "aria-label": "Traceroute hops" } });
  for (const hop of hops) {
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
  return el("div", { className: "trace-result" }, el("p", { className: "trace-summary", text: summary }), list);
}

/** All hosts are visible independently of the selected Summary target. */
export function renderTraceroutes(container, targets, features, selectedId = null) {
  const top = container.scrollTop;
  if (!targets.length) {
    container.replaceChildren(el("p", { className: "empty", text: "No targets match the filter." }));
  } else {
    container.replaceChildren(...targets.map((target) => {
      const address = targetAddress(target);
      return el("section", {
        className: `trace-target${target.id === selectedId ? " is-selected" : ""}`,
        attrs: { "data-target-id": String(target.id), "aria-label": target.host },
      }, el("div", { className: "trace-target-head" },
        el("span", { className: "mtr-target-kind", text: "Target" }),
        el("strong", { className: "cell-text", text: target.host, attrs: { title: target.host } }),
        address && el("span", { className: "trace-target-address mono cell-text", text: address, attrs: { title: target.ip || "" } })),
      tracePath(target, features));
    }));
  }
  container.scrollTop = top;
}

