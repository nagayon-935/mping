// Monitor-row sparklines: the last minute of RTT as a small SVG. The
// geometry is computed by spark.js; this module only builds elements. SVG
// presentation attributes (not inline styles) keep it within the CSP.
import { alignSamples, padTo } from "./timeline.js";
import { robustAxis } from "./scale.js";
import { SPARK_SECONDS, sparkGeometry } from "./spark.js";

const NS = "http://www.w3.org/2000/svg";
export const SPARK_WIDTH = 120;
export const SPARK_HEIGHT = 26;

function svg(tag, attrs) {
  const node = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, String(v));
  return node;
}

/** The last minute of one target's history, on the shared wall-time axis. */
export function lastMinute(samples, intervalMs, now) {
  const points = Math.max(1, Math.ceil((SPARK_SECONDS * 1000) / intervalMs));
  if (samples?.length && typeof samples[0] === "object" && samples[0] !== null) {
    return alignSamples(samples, points, intervalMs, now);
  }
  return padTo(samples ?? [], points);
}

/**
 * Last-minute series per target and one robust scale shared by every row, so
 * row heights compare and a single spike is clipped instead of flattening
 * the others.
 */
export function prepareSparks(targets, history, intervalMs, now) {
  const series = new Map(targets.map((t) => [t.id, lastMinute(history.get(t.id), intervalMs, now)]));
  const axis = robustAxis([...series.values()].flat());
  return { series, max: axis.max };
}

/** An <svg role="img"> sparkline with its accessible label. */
export function sparkline(series, max) {
  const g = sparkGeometry(series, { width: SPARK_WIDTH, height: SPARK_HEIGHT, max });
  const root = svg("svg", {
    class: "spark", viewBox: `0 0 ${SPARK_WIDTH} ${SPARK_HEIGHT}`, width: SPARK_WIDTH, height: SPARK_HEIGHT,
    role: "img", "aria-label": g.label, preserveAspectRatio: "none",
  });
  root.append(svg("title", {}));
  root.firstChild.textContent = g.label;
  root.append(svg("line", { class: "spark-base", x1: 0, y1: SPARK_HEIGHT - 0.5, x2: SPARK_WIDTH, y2: SPARK_HEIGHT - 0.5 }));
  if (g.allLost) {
    const mid = SPARK_HEIGHT / 2;
    root.append(svg("line", { class: "spark-dead", x1: 0, y1: mid, x2: SPARK_WIDTH, y2: mid }));
    return root;
  }
  for (const points of g.runs) root.append(svg("polyline", { class: "spark-line", points }));
  for (const x of g.losses) {
    root.append(svg("line", { class: "spark-loss", x1: x, y1: SPARK_HEIGHT - 7, x2: x, y2: SPARK_HEIGHT }));
  }
  for (const x of g.clipped) {
    root.append(svg("path", { class: "spark-clip", d: `M${x} 0l3 4h-6z` }));
  }
  return root;
}
