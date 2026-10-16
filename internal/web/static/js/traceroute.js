import { el, statusChip } from "./dom.js";

/** TraceRoute exports IP(ASxxx Organization) PTR-name. Keep the parts
 * separate so optional annotations also follow the current feature flags. */
function parseHop(value) {
  if (!value || value === "*") return { reply: false };
  const parts = /^([^\s(]+)(?:\((.*)\))?(?:\s+(.*))?$/.exec(value);
  return { reply: true, address: parts?.[1] ?? value, asn: parts?.[2], ptr: parts?.[3] };
}

export function tracePath(target, features) {
  const values = target.trace_hops ?? [];
  if (!values.length) return el("p", { className: "empty", text: "No traceroute result for this target yet." });
  if (values.length === 1) {
    const value = values[0];
    if (value === "Tracing...") return el("div", { className: "trace-message" }, statusChip("pending", "Tracing…"));
    if (value === "no route found" || value.startsWith("error: ")) return el("div", { className: "trace-message" },
      statusChip("warn", value === "no route found" ? "No route found" : "Traceroute failed"),
      value.startsWith("error: ") && el("p", { text: value.slice(7) }));
  }
  const hops = values.map(parseHop);
  const noReply = hops.filter((hop) => !hop.reply).length;
  const summary = `${hops.length} ${hops.length === 1 ? "hop" : "hops"} · ${noReply} no reply`;
  const list = el("ol", { className: "trace-path", attrs: { "aria-label": "Traceroute hops" } });
  hops.forEach((hop, i) => {
    const destination = hop.reply && [target.ip, target.host].some((ip) => ip && hop.address.toLowerCase() === ip.toLowerCase());
    const identity = el("div", { className: "trace-identity" },
      el("span", { className: "trace-address", text: hop.reply ? hop.address : "No reply" }));
    if (features.ptr && hop.ptr) identity.append(el("span", { className: "trace-annotation", text: hop.ptr }));
    if (features.asn && hop.asn) identity.append(el("span", { className: "trace-annotation", text: hop.asn }));
    list.append(el("li", { className: `trace-hop${hop.reply ? "" : " is-unanswered"}${destination ? " is-destination" : ""}` },
      el("span", { className: "trace-number", text: String(i + 1), attrs: { "aria-label": `Hop ${i + 1}` } }), identity,
      destination && el("span", { className: "trace-tag", text: "Destination" })));
  });
  return el("div", { className: "trace-result" }, el("p", { className: "trace-summary", text: summary }), list);
}

/** All hosts are visible independently of the selected Summary target. */
export function renderTraceroutes(container, targets, features) {
  const top = container.scrollTop;
  if (!targets.length) {
    container.replaceChildren(el("p", { className: "empty", text: "No targets match the filter." }));
  } else {
    container.replaceChildren(...targets.map(target => el("section", {
      className: "trace-target", attrs: { "data-target-id": String(target.id), "aria-label": target.host },
    }, el("div", { className: "trace-target-head" },
      el("strong", { className: "cell-text", text: target.host, attrs: { title: target.host } }),
      el("span", { className: "mono cell-text", text: target.ip || "Resolving address…", attrs: { title: target.ip || "" } })),
    tracePath(target, features))));
  }
  container.scrollTop = top;
}
