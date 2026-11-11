# Web UI column sizing

Tables use a shared `colgroup` and fixed layout. Values cannot resize columns
between updates or after filtering. The target table fills its pane: its host
column receives the width left after the fixed numeric and metadata columns.
The workspace fills the browser width and remaining viewport height on desktop. The HTTP table's URL column
also receives the space left after its fixed columns. If the minimum widths do not fit, only the table
scrolls horizontally.
The page reserves scrollbar space so filtering a long list cannot shift the
columns when the vertical scrollbar disappears. Detail panes do the same.

Widths include cell padding. Numeric text uses a 13px monospace font, with
10px padding on each side.

| Field | Input range / length | Display budget | Standard width |
| --- | --- | --- | --- |
| Status | OK, Warning, Critical, Waiting | One label + one reserved line of explanation (blank for OK; full reason in title and Summary) | 168px |
| Host / PTR | Host input accepts up to 253 UTF-8 bytes; DNS names can be long | One line per name, ellipsis; full name in title and details | At least 200px; flexible |
| IP | IPv4 up to 15 chars; mixed IPv6 notation up to 45, scope suffixes may add more | One line, ellipsis; full address in title and details | 196px |
| AS | `AS4294967295` is 12 chars; organization names have no UI length cap | One line, ellipsis; full text in title and details | 200px |
| Loss | 0–100%, formatted to one decimal | 5 chars (`100.0`) | 68px (96px for MTR icon + number) |
| RTT / jitter | Positive Go durations up to roughly 9.22e12 ms | At most 10 chars including ` ms`; scientific notation from 1e6 ms | 100px |
| Counts | Nonnegative Go integer counts, up to int64 maximum | At most 8 chars; grouped below 1M, M/B/T above, scientific from 1e15 | 88px |
| TTL / hop | 0–255 | 3 digits | 60px |
| RTT · 60s | Last minute of RTT samples | Fixed-size sparkline (no text); accessible label states the range and losses | 144px |
| HTTP URL | No new length restriction | One line, ellipsis; full URL remains in the DOM and title | At least 240px; flexible |
| HTTP code | 3-digit status codes | 3 digits | 64px |
| Port | `65535/udp` | 9 chars | 104px |

Counters and large RTTs retain their unabridged values in titles. Target
summary/details retain unabridged counts and RTT values. Search always uses
the complete underlying strings, not their ellipsized presentation.

## Which columns are shown

The overview always keeps **Status, Host, Loss % and latest RTT**. Extra
columns appear as the viewport or the available pane width grows, in priority
order: RTT · 60s sparkline, Jitter, Sent, Recv, IP, TTL, AS (when enabled).
Columns retain their normal display order. The current RTT header reads
**RTT**, with units in each value. If all configured hosts are IP literals,
the redundant IP column is omitted. This decision uses the full target list,
so filtering and incoming DNS results cannot toggle the column. Widths never
depend on live values.

The MTR table follows the same rule. It always keeps **Hop, Address, Loss %,
RTT and Avg**, then adds Worst, Best, AS (when enabled), Jitter, Sent and Recv
as the pane widens. Only a pane narrower than the required columns scrolls
horizontally.

There is no table-size selector or automatic zoom; column selection uses the
actual available width. The
panes reserve scrollbar space, so filtering or receiving measurements cannot
change column widths. Summary and HTTP tables keep all enabled columns and scroll
horizontally when needed.

The monitor shows current RTT and a one-minute sparkline; average/minimum/peak
live in Summary. The larger RTT history chart is in Summary only, with a
numeric RTT at the cursor.

On desktop (1100px and up), a two-row grid fills the space below the header.
The monitor fills the workspace; after selection, Summary takes the right
column at full height (7:5 widths). With a route option, the route pane takes
the lower row under the monitor.
Every desktop pane takes its grid cell's height and scrolls internally. Narrower
windows stack the panes. Phone widths are not supported: below 720px the page
scrolls horizontally instead of compressing the layout. AS is omitted unless
enabled, including in
the selected target's details; PTR annotations and PMTU details follow their
respective options as well.

Target rows are 48px tall, with one-line host/PTR and one reserved explanation
line so changing health cannot move the following rows. HTTP/MTR/port rows
are 44px. Exceptionally long group headings are ellipsized. Abnormal overview
values use a small tinted badge rather than coloring the whole cell.

## Summary and global pages

Summary is the only selected-host detail view; there are no tabs. It contains
the reason, KPI tiles, the RTT chart, recent events, and (under Details)
statistics, enabled port checks and authorized target actions. It retains its
height and action-button state during live measurements. Log is an independent
`#log` page with the latest 200 events across all targets, unaffected by host
selection or the monitor filter. HTTP checks use the independent `#http` page
when enabled. Page navigation retains Summary selection and monitor/route
scroll positions. Log and HTTP preserve selected text and scroll during updates.

## Route displays

- Traceroute enabled, MTR disabled: standalone Traceroute pane.
- MTR enabled: standalone MTR pane, even when Traceroute is also enabled.
- Neither enabled: no route pane.

Both route views show all filtered hosts without requiring selection. Desktop
route panes fill their grid cell; stacked panes reserve 420px.
MTR has one two-axis scroll viewport with 36px sticky column headings and 64px
sticky target headings showing host, address and hop count. Target identity stays
visible when the numeric columns are scrolled horizontally. Numeric widths
remain fixed. AS appears only when enabled. Selecting or clearing a host does not
change MTR membership or scroll positions. Probes use TTL/Hop Limit-limited ICMP
Echo for IPv4/IPv6, not UDP.

Traceroute uses a numbered vertical path for each target, with wrapping addresses
and optional AS/PTR annotations. Empty and `*` hops are unanswered; progress and
error states are separate from the hop list. A responder matching the target IP
has a Destination label. Target headings remain visible during vertical scroll.

The Summary RTT chart uses a robust vertical scale (top just above the p95,
clipping higher peaks with a `▲` marker). The tick interval is rounded to
1/2/5 × 10^k, with four equal intervals.

## Live ordering and interaction stability

Health filters and text search apply to the monitor and routes.
Sorting uses a snapshot of target IDs across groups; incoming values do not
reorder existing rows. New targets append, deleted IDs disappear, and Refresh
order explicitly recomputes ranking. Configured order restores group headings.
Headers expose sort direction, and keyboard focus survives table rebuilds.

The header is a single row with health filter pills, connection/measurement
state and actions.
Notifications overlay the workspace; chart captions, numeric readouts and
sample descriptions reserve their height even when idle. Route
Selected host buttons scroll only their own pane. Graph sample inspection
works with pointer or keyboard and announces only explicit keyboard actions.
Destructive actions use native modal dialogs with Cancel focused initially.
