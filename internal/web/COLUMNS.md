# Web UI column sizing

Tables use a shared `colgroup` and fixed layout. Values cannot resize columns
between updates or after filtering. The target table's host column takes spare
width only up to a cap (`--host-grow`, 80px); any further width stays empty on
the right. The HTTP table's URL column receives all the space left after its
fixed columns. If the minimum widths do not fit, only the table
scrolls horizontally.
The page reserves scrollbar space so filtering a long list cannot shift the
columns when the vertical scrollbar disappears. Detail panes do the same.

Widths include cell padding. Numeric text uses a 13px monospace font, with
10px padding on each side; compact mobile cells use 12px text and 4px padding.

| Field | Input range / length | Display budget | Standard width |
| --- | --- | --- | --- |
| Status | OK, Warn, Crit, Waiting | One label + at most two lines of explanation | 152px |
| Host / PTR | Host input accepts up to 253 UTF-8 bytes; DNS names can be long | One line per name, ellipsis; full name in title and details | 200px, growing to at most 280px (40px more than the compact width on phones) |
| IP | IPv4 up to 15 chars; mixed IPv6 notation up to 45, scope suffixes may add more | One line, ellipsis; full address in title and details | 196px |
| AS | `AS4294967295` is 12 chars; organization names have no UI length cap | One line, ellipsis; full text in title and details | 200px |
| Loss | 0–100%, formatted to one decimal | 5 chars (`100.0`) | 68px (96px for MTR icon + number) |
| RTT / jitter | Positive Go durations up to roughly 9.22e12 ms | At most 10 chars including ` ms`; scientific notation from 1e6 ms | 100px |
| Counts | Nonnegative Go integer counts, up to int64 maximum | At most 8 chars; grouped below 1M, M/B/T above, scientific from 1e15 | 88px |
| TTL / hop | 0–255 | 3 digits | 60px |
| DSCP | Named codepoints up to 4 chars, or 0–63 | 4 chars | 72px |
| Trend | 60 samples | Canvas uses available inner width | 140px |
| HTTP URL | No new length restriction | One line, ellipsis; full URL remains in the DOM and title | At least 240px; flexible |
| HTTP code | 3-digit status codes | 3 digits | 64px |
| Port | `65535/udp` | 9 chars | 104px |

Counters and large RTTs retain their unabridged values in titles. Target
summary/details retain unabridged counts and RTT values. Search always uses
the complete underlying strings, not their ellipsized presentation.

## Which columns are shown

There is no column toggle. The target table shows as many columns as fit in
its current width and drops the least important ones first. Status and host
identify a row and are always kept; the rest are added in this order and the
first one that does not fit ends the list (a narrower, less important column
never takes the place of a dropped one):

1. Loss %  2. Last  3. Avg  4. Jitter  5. RTT trend  6. IP  7. Min  8. Peak
9. Sent  10. Recv  11. TTL  12. AS (when enabled)  13. DSCP (when enabled)

Columns keep their normal display order, whatever their priority. The set is
recomputed when the window, the docked detail panel or the scrollbar changes
the table's width, using the widths in the table above (as overridden below).
Only if status and host alone do not fit does the table scroll horizontally.
The HTTP and detail tables always show all their columns.

Whatever width is left after choosing columns is turned into a larger table:
the whole target table (text, columns and row heights) is zoomed by the ratio of
the wrapper width to the widest the chosen columns can be, never below 1 and at
most 2. Beyond that (very large monitors) the remainder stays empty. A column
appearing as the screen widens resets the zoom to about 1.

Docked next to the detail panel (screens of 1100px and up) the host minimum is
180px and the trend is 112px. On screens up to 640px every column uses the
compact widths: status 72, host 94, loss 46, RTT 80 and trend 60 (4px cell
padding, 12px text); up to 360px the status and host shrink to 68 and 90.
Status, host, loss and latest RTT need 292px (72 + 94 + 46 + 80), so a phone
keeps at least those, plus average RTT when it fits.

Target rows are 76px tall, with one-line host/PTR and two-line explanations;
HTTP/MTR/port rows are 44px. Exceptionally long group headings are ellipsized.
