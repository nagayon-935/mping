# TUI reference

← [README](../../README.md)

## Key bindings

| Key | Action |
| :--- | :--- |
| **q** | Quit the application |
| **s** | Stop all measurements and preserve their last statistics |
| **S** | Restart measurements with new workers, preserving statistics (after **s**) |
| **R** | Reset statistics and UI logs; remain stopped if measurements are stopped |
| **a** | Open "Add host" dialog — type a hostname or IP and press Enter to add it at runtime |
| **d** | Confirm deletion of the selected target, or the target currently shown in details |
| **Tab** | Cycle focus: Ping Monitor → Traceroute or MTR Monitor → Port Monitor → HTTP Monitor → RTT Graphs → Log |
| **↑ / ↓ / PgUp / PgDn** | Select hosts in Ping Monitor; scroll other panes and host details |
| **Enter** | Open selected host details: statistics, routes, MTR, ports, graphs, and target events |
| **Esc** | Return from details, or restore the layout after maximizing a pane |
| **f** | Fold or expand the focused pane |
| **z** | Maximize the focused pane or restore its previous layout |
| **w** | Save a session report from the overview, or one target from details |
| **b** | Show the Web UI control URL and copy it (`--web` only) |

Deletion opens a confirmation showing the host, target ID, and IP. **Cancel** (`キャンセル`) is selected initially; Enter cancels. Use Tab or the left/right arrows to select **Delete** (`削除`), then Enter to confirm. Esc cancels, and repeated `d` does not confirm. Measurements continue while the dialog is open. Cancelling restores the previous focus and scroll position. Re-adding a deleted target starts a new measurement. For the last remaining target, the dialog explains that **q** exits mping; **Enter** or **Esc** returns to monitoring.

Pane folding and maximization are available in the overview. Folded title rows remain reachable with Tab; press `f` to expand them. Measurements continue while panes are hidden. Tab stays within the maximized pane until it is restored.

> Live host edits preserve surviving targets' statistics, graphs, routes, port results, and independent HTTP checks. Editing a stopped session does not resume it. Re-adding a deleted host starts a new measurement. Existing `--count` budgets and the session's `--duration` deadline are preserved. Outstanding probes cancelled by deletion or stopping are exported as `cancelled`, not packet loss. YAML changes to groups, display names, health rules and target membership also retain surviving targets. Changes to probe/network settings rebuild measurements, with the changed settings logged.
>
> Selection and deletion use stable target IDs, including duplicate host names. Echo IDs are never recycled within a session; after 32,768 worker allocations across initial starts, additions, and restarts, restart mping to obtain a fresh session.

Stop, restart and reset operations execute in keypress order. A running reset discards results from probes sent before the reset and recreates enabled route/port/HTTP monitors. It keeps the remaining `--count` budget; restarting starts a fresh budget. When stopped, reset clears ping and MTR counters while preserving the last route and port/HTTP results. Quitting waits for measurement workers and prints a terminal summary. Reports are saved explicitly with **w** or the Web UI's **Report** button.

## Saving investigation results

Press **w**, enter a new file path ending in **.json** or **.csv**, and press **Enter** or activate **Save**. The format is inferred from the extension and displayed below the input; no format selector is needed. A missing extension defaults to `.json`; other extensions are rejected. **Tab** moves between fields and buttons; **Esc** cancels. Saving from the overview includes all active targets, retained final results of removed targets, and independent HTTP checks. Saving from host details includes only that target. Measurements continue during capture and writing; completion or errors appear in the footer and Log.

JSON reports include the session and collection start times, capture interval, stable target IDs, ping statistics window, effective settings, DNS IP changes, Ping, traceroute, MTR, port results, and retained target events. They use `schema_version: 1`; fields ending in `_ms` are milliseconds. `statistics` reuses the existing JSON target summary, including its `loss_rate_pct` denominator (sent probes) and separate cancelled probes.

CSV reports are a table with one row per target — active targets first, then removed ones with `removed_at` set — for spreadsheets. Columns: `captured_at`, `target_id`, `host`, `ip`, `group`, `removed_at`, `measurement_state`, `health_status`, `health_reason`, `sent`, `received`, `lost`, `loss_pct`, `recent_loss_pct`, `duplicates`, `late_replies`, `last_rtt_ms`, `min_rtt_ms`, `avg_rtt_ms`, `max_rtt_ms`, `jitter_ms`, `last_ttl`, `last_error`, `last_loss_at`, `last_reply_at`, `started_at`, `statistics_since`, `asn`, `org`, `ptr`, `route_changes`, `path_mtu`, `pmtu_payload`, `pmtu_status` and `pmtu_source`. RTT and TTL columns stay empty until a target has replied. Ports, HTTP checks, hops and events are only in JSON. The file is UTF-8 with a byte order mark so spreadsheet applications read non-ASCII names; text cells starting with `=`, `+`, `-` or `@` get a leading `'` so they are not evaluated as formulas.

The most recent 128 removed targets, 128 events per target, and 64 destination IP history entries are retained; reports include omitted-entry counts. A reload that rebuilds probe/network settings starts a new collection and clears earlier targets and removal history; live metadata/membership reloads retain them. Manual reset starts a new ping statistics window; when stopped, port/HTTP results retain their previous counters. Measurements and auxiliary checks are captured sequentially within the recorded capture interval. Reports contain aggregate results and retained events, not individual ping records.

Reports are saved with owner-only read/write permissions (`0600`). When launched through sudo, ownership is assigned to the user who invoked sudo; setuid installations create the report as the invoking user. Ownership is set before publication; failure leaves no final report.

Existing files, including symlink destinations, are preserved. Each save writes a temporary file in the destination directory and atomically publishes the complete new file. On a save error, choose a new writable path and retry with **w**.

## RTT Graphs and Log panes

* **RTT Graphs** — one chart per target with an auto-scaling Y-axis (expands immediately on a spike, shrinks after a hold period) and roughly 30 seconds of recent samples at the configured interval on the X-axis. ICMP and TCP/UDP port series are both supported. Lost probes are drawn as a red `×` on the zero line and columns where only some probes were lost as orange bars (the same colors as the **RTT 60s** sparkline); a chart header shows the window's loss, e.g. `loss 20% (6/30)`. The pane takes the spare rows left once the Ping Monitor has room for 12 hosts, the Log keeping its base height; it shows up to six charts at once and scrolls for more. Press **f** on the pane to fold or open it.
* **Log** — records threshold alerts and status changes. The same status change hitting several targets in the same second is logged once with the targets it hit (e.g. `[12:09:03] crit: Jitter > 50.0ms ×3 — 1.1.1.1, 8.8.8.8, 9.9.9.9`).

## TUI columns

* **SRC** — The bound source address, or the automatically detected source-address hint for the destination's address family.
* **DST** — Resolved destination IP. Shown as `domain (IP)` when a hostname is given.
* **DNS** — DNS server used to resolve the target (shown only when some target has one).
* **ASN** — Autonomous System Number and organization name of the target IP (enabled with `-a`). Example: `AS15169 Google LLC`.
* **PTR** — Reverse DNS name of the target IP (enabled with `-r`).
* **RCV** — Number of packets received successfully.
* **LOST** — Number of lost packets.
* **LOSS%** — Packet loss percentage. Colour boundaries are configurable (defaults shown).
  * **Green**: 0%–20% &nbsp;|&nbsp; **Orange**: >20% and ≤80% &nbsp;|&nbsp; **Vivid red**: >80%
* **RTT / AVG / JIT** — Latest / average / jitter round-trip time. Colour boundaries are configurable (defaults shown).
  * **RTT**: Green (≤50 ms) / Orange (≤200 ms) / Red (>200 ms)
  * **JIT**: Green (≤10 ms) / Orange (≤50 ms) / Red (>50 ms)
  * Override with `--rtt-warn/--rtt-crit`, `--jitter-warn/--jitter-crit`, `--loss-warn/--loss-crit`, or the `thresholds:` YAML block.
* **SIZE** — Payload size of sent packets.
* **MTU** — MTU of the specified or initially inferred outbound interface. With `-m` the column is **PMTU** and shows the discovered path MTU.
* **RTT 60s** — Sparkline of the last 60 seconds of RTT (`▲` above the row's p95 scale). Cells are cyan without loss, orange when some probes in them were lost, and a red `×` when all were.
* **TTL** — IPv4 TTL or IPv6 Hop Limit of the last reply.
* **ERR** — Abbreviated latest error message (red), shown only while some target has one. Full details appear in the Log pane.
* **LAST LOSS** — Time elapsed since the last packet loss.

### Narrow terminals

Cells are not cut short. When the terminal is too narrow for every column, whole columns are hidden instead, in this order:

1. Supplementary columns: SIZE → MTU → SRC → DNS → LAST LOSS → LOST → RCV → TTL → AVG → RTT 60s (the sparkline goes last: it is the only RTT trend for hosts past the six the graph pane shows at once) (SIZE, MTU, SRC and DNS only when their option was not given).
2. Columns requested with an option: SIZE (`-s`) → SRC (`-S` / `-I`) → DNS (`-d`) → ASN (`-a`) → PTR (`-r`) → PMTU (`-m`).

DST, LOSS%, RTT and JIT (and ERR while an error is shown) are never hidden. The pane title lists hidden columns; a hidden setting every row shares, such as the payload size or a pinned source address, appears there with its value (`Ping Monitor · SIZE 56 · hidden: TTL AVG`). Every value stays available in the host details (Enter).
