# Route, HTTP, port and PMTU monitors

← [README](../../README.md)

## Traceroute Monitor pane

* Shown only when `-T` / `--traceroute` is given. `-T` and `-M` cannot be combined: `-T` discovers the route only, `-M` also measures every hop.
* Switching between `traceroute: true` and `mtr: true` in the hosts file applies on reload without restarting measurements: ping counters, graphs and history are kept and the pane switches in place. Turning route monitoring on or off restarts measurements.
* The route (up to 30 hops) is discovered at startup and rediscovered every 10 minutes, with no per-hop probes in between — about 30 probes per target every 10 minutes.
* Results are shown in a Host / Hops / Init TTL / Route table, one row per target. Hops carry the AS (`-a`) and PTR name (`-r`) when enabled; `*` is a hop that did not answer.
* **Route change detection** works as in the MTR pane: a changed path adds a `[FLAP ×N HH:MM:SS]` line under the route and a yellow alert in the Log pane.
* When the route cannot be discovered (for example, the target does not resolve), the pane shows why and retries at the next rediscovery.

## MTR Monitor pane

* Shown only when `-M` / `--mtr` is given.
* Performs continuous TTL-limited ICMP probing to every hop on the path to each target.
* The hop path is discovered at startup and re-discovered every 10 minutes to track route changes. Hops that miss discovery are still probed each round and appear as soon as they respond. Route discovery probes the monitored IP, avoiding an independent DNS choice when comparing paths.
* Each hop is probed once per second. Unresponsive hops (`*`) accumulate 100% loss.
* The header row shows `SrcIP -> DstIP` (or `hostname (SrcIP -> DstIP)` for hostname targets).
* Columns:
  * **Hop** — TTL hop number
  * **Host** — Responder IP, followed by its PTR name when `-r` is given and its AS when `-a` is given (e.g., `8.8.8.8 dns.google (AS15169 Google LLC)`). `*` when no response.
  * **Loss%** — Packet loss percentage for this hop (green / orange / red)
  * **Snt** — Total probes sent
  * **Recv** — Total replies received
  * **Last** — RTT of the most recent probe
  * **Avg** — Average RTT
  * **Min** — Minimum RTT
  * **Max** — Maximum RTT
  * **Jitter** — Smoothed inter-packet delay variation (RFC 1889)
* On narrow terminals, **Min**, **Max**, **Recv**, and **Jitter** columns are hidden automatically (compact mode).
* Hops are included in statistics snapshots as `mtr_hops` per target in both modes (with `-T`, their counters stay at zero), and a route discovery failure as `mtr_error`.
* **Route flap detection** — when re-discovery detects that the hop path has changed, a `[FLAP ×N HH:MM:SS]` badge is appended to the target's header row and a yellow alert is written to the Log pane (e.g. `[route flap google.com: hop 3: 10.0.0.2 → 10.0.0.9]`).

## HTTP Monitor pane

* Shown only when `-H` / `--http` is given.
* Performs HTTP(S) GET requests at the ping interval and records the HTTP status code and response time.
* Multiple URLs can be specified comma-separated (e.g. `-H https://a.example.com,https://b.example.com`) or with repeated flags.
* Can also be set in the `http:` list in the YAML hosts file.
* Columns:
  * **URL** — the monitored endpoint
  * **Status** — `Up` (green, 2xx–3xx) / `Down` (red, 4xx–5xx) / `Error` (red, connection error) / `Checking...` (gray, initial state)
  * **Code** — HTTP status code (e.g. `200`, `503`); `-` on error
  * **Last** — response time of the most recent request
  * **Min** — minimum response time (Up responses only)
  * **Avg** — average response time (Up responses only)
  * **Max** — maximum response time (Up responses only)
  * **Up** — cumulative Up count
  * **Down** — cumulative Down + Error count
  * **Since** — time elapsed since the last status change
* On narrow terminals, **Min**, **Avg**, **Max**, and **Since** columns are hidden automatically (compact mode).
* Status changes are logged to the Log pane (e.g. `HTTP https://example.com: Up → Down`).
* HTTP check results are included in statistics snapshots as `http_checks`.

## Port Monitor pane

* Shown only when `-p` / `--port` is given.
* Performs TCP/UDP reachability checks in real time at the ping interval.
* Multiple ports can be specified comma-separated (e.g. `-p 443/tcp,53/udp`).
* Omitting the protocol defaults to TCP (e.g. `-p 443` → `443/tcp`).
* UDP sends an empty datagram. A reply is `Open`, an ICMP Port Unreachable or similar receive error is `Closed`, and no reply is `Open|Filtered`. This does not validate an application protocol such as DNS.
* Columns:
  * **Target** — Hostname
  * **Port** — Port number and protocol (e.g. `443/tcp`)
  * **Service** — Estimated service name (`Unknown` if not recognized)
  * **Status** — Green `Open` / Red `Closed` / Yellow `Filtered` or `Open|Filtered`
  * **Last / Min / Avg / Max** — Latest, minimum, average and maximum RTT; Min/Avg/Max use Open results only.
  * **Open/Closed** — Cumulative Open count / Closed+Filtered+Open|Filtered count
  * **Last Change** — Time elapsed since the last status change

## PMTU discovery

* `--discovery-mtu` / `-m` is the common PMTU switch. It resolves each destination independently and dispatches to the IPv4 or IPv6 implementation using the resolved IP. `-4` / `-6` are not required for PMTU discovery.
* IPv4 runs DF probe searches in parallel at startup (up to eight targets). The initial payload upper bound is the detected interface MTU minus 28 bytes, or 9872 bytes when the interface MTU is unknown. When all resolved targets are IPv4, **Size** uses the smallest successful payload limit. Unreachable, ICMP-filtered, and unresolvable targets are excluded and receive no PMTU result.
* IPv6 discovery runs in the background after Ping starts; Path MTU initially shows **Unknown** and fills in as discovery completes. On macOS/Linux it uses the OS path-MTU estimate. It disables sender fragmentation, sends an echo at that size, and retries only if the OS lowers its estimate. An exact echo reply marks the result **confirmed**; otherwise it remains **estimated**. A timeout alone never lowers the estimate. An OS estimate may initially be just the outgoing interface MTU.
* TUI details and Web Summary show the IP-packet **PMTU**, ICMP **payload** limit, and confirmation status. IPv6 and mixed-family runs keep the configured regular Ping size. A changed destination IP clears the old path's result; restarting measurement setup discovers it again.
* JSON retains `pmtu` as the payload limit and adds `path_mtu` (whole IP packet), `pmtu_source` (`os` / `probe`), and `pmtu_status` (`estimated` / `confirmed` for IPv6, `probed` for IPv4). CSV includes the same information, with `pmtu_payload` for the payload column. For example, IPv6 PMTU 1450 allows 1402 bytes of ICMP data without extension headers.
* IPv6 PMTU discovery is unsupported on other OSes; it reports a discovery error and continues ordinary Ping. Received `Packet Too Big` errors and their advertised MTU are also recorded by ordinary IPv6 Ping.
