// Use the browser/OS timezone, independently of its preferred 12/24h clock.
// Do not supply a fixed timezone or infer one from the response's UTC offset.
function parts(value, date = false) {
  const at = new Date(value);
  if (!value || !Number.isFinite(at.getTime())) return null;
  const options = { hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" };
  if (date) Object.assign(options, { year: "numeric", month: "2-digit", day: "2-digit" });
  return Object.fromEntries(new Intl.DateTimeFormat("en-GB", options).formatToParts(at).map((p) => [p.type, p.value]));
}

export function formatTime(value) {
  const p = parts(value);
  return p ? `${p.hour}:${p.minute}:${p.second}` : "–";
}

export function formatDateTime(value) {
  const p = parts(value, true);
  return p ? `${p.year}-${p.month}-${p.day} ${p.hour}:${p.minute}:${p.second}` : "–";
}
