<p align="center">
  <img src="./assets/logo.jpg" alt="mping logo" width="200">
</p>

# mping

**mping** is a terminal-based multi-target ping tool written in Go. It pings multiple hosts simultaneously and displays real-time statistics — packet loss, RTT, TTL, and more — in a clean TUI (Text User Interface).

![Go Version1.26](https://img.shields.io/badge/go-v1.26-blue "Go Version1.26")![MIT License](https://img.shields.io/badge/license-MIT-blue "MIT License")[![Coverage Status](https://coveralls.io/repos/github/nagayon-935/mping/badge.svg?branch=main)](https://coveralls.io/github/nagayon-935/mping?branch=main)

[日本語](./README.ja.md)

## Features

* **Multi-target ping** — monitor multiple hosts concurrently in a single view.
* **Real-time statistics** — packet loss, RTT, TTL, and error messages updated live.
* **TUI dashboard** — high-visibility table on a black background. Column widths are distributed automatically based on terminal width; switches to a compact 2-row-per-target layout when the window is narrow.
* **Color-coded alerts** — loss ratio, RTT, and Jitter are color-coded for instant status recognition. Alerts are recorded in the Log pane when thresholds are exceeded.
* **Flexible configuration** — specify interface, source IP, packet size, send count, and more.
* **YAML host list** — manage target hosts in a file.
* **Host groups** — define named groups in the YAML file. Each group shows a header row with a member count, making it easy to visually separate sets of targets.
* **Traceroute pane** — show the route to each target in a Host/Route table when `-T` is given. Multiple targets are traced concurrently and displayed together.
* **MTR Monitor pane** — continuous per-hop loss/latency statistics when `-M` is given. Each hop is probed every second and displays Hop, Host, Loss%, Snt, Recv, Last, Avg, Min, Max, Jitter — the same columns as `mtr`. Can be used simultaneously with `-T`.
* **HTTP(S) health check pane** — monitor HTTP/HTTPS endpoints when `-H` is given. Performs GET requests at the ping interval and tracks status code, response time (Last/Min/Avg/Max), and cumulative Up/Down counts. Status changes are logged to the Log pane.
* **Port Monitor pane** — monitor TCP/UDP port reachability in real time with `-p`. Displays the estimated service name, **Last / Min / Avg / Max RTT** (measured from TCP connect or UDP round-trip), cumulative Open/Closed counts, and time since last status change. RTT statistics are collected for `Open` responses only.
* **PMTU discovery** — probe maximum payload size using DF-bit ICMP packets.
* **Auto source IP detection** — automatically detects and displays the local IP used for each destination.
* **RTT graph** — auto-scaling Y-axis (expands immediately on spike, shrinks after a hold period) × 30 seconds (X-axis) per target. Supports both ICMP and TCP/UDP port series.
* **CSV log output** — save results with statistics to a file.
* **JSON statistics export** — write a live snapshot of all statistics to a JSON file every 5 seconds with `-j`.

## Supported platforms

| OS | Architecture | Notes |
| :--- | :--- | :--- |
| Linux | amd64, arm64 | Recommended: grant `CAP_NET_RAW` via `setcap` |
| macOS | amd64, arm64 (Apple Silicon) | Uses `setuid` |

> **Privileges required** — mping uses raw ICMP sockets to obtain accurate TTL values. On Linux the preferred approach is granting `CAP_NET_RAW` with `setcap`; `install.sh` handles this automatically. On macOS a `setuid` bit is set instead. With a `setuid` install, mping keeps root only for opening raw sockets: every file it reads or writes (`-f`, `include:`, `-o`, `-j`, saved reports) is accessed with the invoking user's permissions, so it cannot be used to read or modify files that user couldn't access directly.

> **Terminal Compatibility** — Standard terminals on Linux and macOS may not render colors correctly. If you experience issues with color display, consider using a modern terminal emulator (e.g., iTerm2, Alacritty, or kitty).

## Installation

### Pre-built binary (recommended)

Download the archive for your platform from the [Releases](https://github.com/nagayon-935/mping/releases) page and run the bundled `install.sh`.

#### Linux (amd64)

```bash
# Download and extract (v0.4.5 shown; check the Releases page for the latest version)
curl -LO https://github.com/nagayon-935/mping/releases/download/v0.4.5/mping-v0.4.5-linux-amd64.tar.gz
tar -xzf mping-v0.4.5-linux-amd64.tar.gz

# Install (grants CAP_NET_RAW via setcap; falls back to setuid if setcap is unavailable)
sudo ./install.sh
```

#### Linux (arm64 — e.g. Raspberry Pi, AWS Graviton)

```bash
curl -LO https://github.com/nagayon-935/mping/releases/download/v0.4.5/mping-v0.4.5-linux-arm64.tar.gz
tar -xzf mping-v0.4.5-linux-arm64.tar.gz
sudo ./install.sh
```

#### macOS (Intel)

```bash
curl -LO https://github.com/nagayon-935/mping/releases/download/v0.4.5/mping-v0.4.5-darwin-amd64.tar.gz
tar -xzf mping-v0.4.5-darwin-amd64.tar.gz
sudo ./install.sh
```

#### macOS (Apple Silicon)

```bash
curl -LO https://github.com/nagayon-935/mping/releases/download/v0.4.5/mping-v0.4.5-darwin-arm64.tar.gz
tar -xzf mping-v0.4.5-darwin-arm64.tar.gz
sudo ./install.sh
```

`install.sh` copies the binary to `INSTALL_DIR` (default: `/usr/local/bin`) and sets the appropriate privilege:

* **Linux** — `setcap cap_net_raw+ep` (falls back to `setuid` if `setcap` is not available)
* **macOS** — `chown root` + `chmod u+s` (setuid)

To install to a different directory:

```bash
sudo INSTALL_DIR=/usr/local/bin ./install.sh
```

After installation, run mping **without** `sudo`:

```bash
mping google.com 1.1.1.1
```

---

### Build from source

**Requirements:** Go 1.26.6 or later

```bash
git clone https://github.com/nagayon-935/mping.git
cd mping
```

#### Using make

```bash
# Build only
make build

# Build + install (setuid on macOS; setcap on Linux when available)
make install
```

`make install` rebuilds the binary and runs `sudo ./install.sh`, which installs it to `/usr/local/bin/mping` by default and grants the platform-specific privileges described above. If another `mping` appears earlier in your PATH, that copy will still run: check with `type -a mping`. After moving or removing an old copy, clear the shell's command cache with `hash -r`.

#### Using go build directly

```bash
go build -o mping ./cmd/main
sudo ./install.sh
```

## Usage

```bash
# Basic (no sudo needed after install.sh)
mping google.com 1.1.1.1 8.8.8.8

# Specify network interface
mping -I eth0 google.com

# Set packet size (100 bytes) and count (10 packets)
mping -s 100 -c 10 google.com

# Save results to a CSV file
mping -o results.csv google.com

# Load hosts from a YAML file
mping -f hosts.yaml

# Force IPv4 only
mping -4 google.com

# Force IPv6 only
mping -6 google.com

# Show Traceroute pane
mping -T google.com

# PMTU discovery (probes from payload size 9872 downward)
mping -m google.com

# TCP port reachability check (443/tcp)
mping -p 443/tcp google.com

# Multiple ports (comma-separated)
mping -p 443/tcp,53/udp google.com 8.8.8.8

# MTR-style per-hop monitor
mping -M google.com

# MTR + Traceroute simultaneously
mping -T -M google.com

# HTTP(S) health check
mping -H https://example.com/health google.com

# Multiple HTTP endpoints
mping -H https://api.example.com/health,https://cdn.example.com/ping google.com

# Traceroute + Port Monitor simultaneously
mping -T -p 443/tcp google.com

# Export live statistics to a JSON file (updated every 5 s)
mping -j stats.json google.com 1.1.1.1

# Customise colour-coding thresholds (warn = orange, crit = red)
mping --rtt-warn 30 --rtt-crit 100 --loss-warn 10 --loss-crit 50 google.com

# Display AS numbers for target IPs
mping -a google.com 1.1.1.1
```

> If you run mping **without** installing (i.e. without `setcap`/`setuid`), prepend `sudo`:
> ```bash
> sudo ./mping google.com
> ```

### hosts.yaml example

List hosts under the `hosts:` key. Options specified here are overridden by explicit CLI flags.

```yaml
hosts:
  - google.com
  - 1.1.1.1
interval: 500
timeout: 2000
traceroute: true
mtr: true
asn: true
port:
  - 443/tcp
  - 53/udp
json-output: stats.json
dns-server: 8.8.8.8
resolve-all: true
thresholds:
  rtt-warn: 50      # ms (orange)
  rtt-crit: 200     # ms (red)
  jitter-warn: 10   # ms (orange)
  jitter-crit: 50   # ms (red)
  loss-warn: 20     # percent (orange)
  loss-crit: 80     # percent (red)
```

### Host groups in YAML

Use the `groups:` key to define named groups of hosts. Each group is rendered with a header row and a worst-case aggregate row. Groups can coexist with ungrouped `hosts:` entries; ungrouped hosts are displayed first.

```yaml
hosts:
  - 8.8.8.8        # ungrouped — displayed above all groups

groups:
  - name: US DNS
    hosts:
      - 1.1.1.1
      - 8.8.4.4
  - name: Japan
    hosts:
      - dns.google
      - dns.cloudflare.com
```

See [examples/hosts-groups.yaml](examples/hosts-groups.yaml) for a runnable example (`mping -f examples/hosts-groups.yaml`).

### Adding many hosts: patterns, names, and include files

Host entries (in `hosts:`, `groups[].hosts:`, and include files) may be patterns that expand to several hosts:

| Pattern | Expands to |
|---|---|
| `10.0.0.1-20` | `10.0.0.1` … `10.0.0.20` (last octet) |
| `10.0.0.250-10.0.1.5` | every IPv4 address in the range |
| `192.0.2.0/28` | `192.0.2.1` … `192.0.2.14` (IPv4 /30 and wider skip the network and broadcast addresses; IPv6 CIDRs keep every address) |
| `core-sw{01..12}.lab` | `core-sw01.lab` … `core-sw12.lab` (zero padding is kept; several `{N..M}` form a product) |

A single pattern may expand to at most 1024 hosts and a whole hosts file (including include files) to at most 4096 targets. IPv6 ranges are not supported — use CIDR notation.

Give a host a display `name` to show it instead of the address (the IP is still shown next to it). Names must be unique and must not equal another entry's host; a name cannot be used on a pattern that expands to several hosts. The name also replaces the host in CSV/JSON output and reports (the resolved IP is still recorded).

```yaml
hosts:
  - {host: 10.0.0.1, name: core-sw01}
```

`include:` (top level or per group) appends hosts from text/CSV files, resolved relative to the YAML file. Each line is `host[,name[,dscp]]`; `#` starts a comment and a first line whose first column is `host` or `hostname` is treated as a header. Every host must be an IP address or a hostname (letters, digits, `.`, `-`, `_`), and the file must be readable by the user running mping. Include files are watched for changes like the YAML file itself, including ones added to the YAML before they exist.

```yaml
groups:
  - name: Core
    hosts: ["core-sw{01..04}.lab"]
  - name: Branches
    include: [branches.csv, more-branches.txt]
```

See [examples/hosts-patterns.yaml](examples/hosts-patterns.yaml) and [examples/branches.csv](examples/branches.csv).

### Options

| Flag | Short | Description | Default |
| :--- | :--- | :--- | :--- |
| `--interval` | `-i` | Ping send interval (ms) | `1000` |
| `--timeout` | `-t` | Ping timeout (ms) | `1000` |
| `--file` | `-f` | Path to YAML host list file | `""` |
| `--traceroute` | `-T` | Show Traceroute pane | `false` |
| `--mtr` | `-M` | Show MTR Monitor pane (continuous per-hop loss/latency) | `false` |
| `--dns-server` | `-d` | Custom DNS server IP address to resolve target hostnames | `""` (OS default) |
| `--resolve-all` | | Resolve target hostnames to all IP addresses and monitor them concurrently | `false` |
| `--discovery-mtu` | `-m` | Discover max payload size with DF bit | `false` |
| `--interface` | `-I` | Network interface name (e.g. `eth0`, `en0`) | `""` |
| `--source` | `-S` | Source IPv4 address | `""` (auto-detect) |
| `--size` | `-s` | Payload size in bytes | `56` |
| `--count` | `-c` | Number of packets per target (0 = unlimited) | `0` |
| `--ipv4` | `-4` | Use IPv4 only | `false` |
| `--ipv6` | `-6` | Use IPv6 only | `false` |
| `--output` | `-o` | CSV log output file path | `""` |
| `--port` | `-p` | Ports to check (e.g. `443/tcp`, `53/udp`, `443`). Comma-separated for multiple. | `""` |
| `--json-output` | `-j` | Write a JSON statistics snapshot to this file every 5 seconds | `""` |
| `--asn` | `-a` | Look up and display AS numbers for target IPs | `false` |
| `--http` | `-H` | URL(s) to health-check, e.g. `https://example.com/health`. Comma-separated or repeated for multiple. | `""` |
| `--rtt-warn` | | RTT warn threshold in ms (orange) | `50` |
| `--rtt-crit` | | RTT crit threshold in ms (red) | `200` |
| `--jitter-warn` | | Jitter warn threshold in ms (orange) | `10` |
| `--jitter-crit` | | Jitter crit threshold in ms (red) | `50` |
| `--loss-warn` | | Loss warn threshold in percent (orange) | `20` |
| `--loss-crit` | | Loss crit threshold in percent (red) | `80` |

> **Thresholds** — `warn` is the orange boundary and `crit` the red boundary for colour-coding the Loss Ratio, RTT, and Jitter columns (and for triggering alert log entries). For each metric `warn` must be less than `crit`. These can also be set in the `thresholds:` block of the YAML file.

### Key bindings

| Key | Action |
| :--- | :--- |
| **q** | Quit the application |
| **s** | Stop all measurements and preserve their last statistics |
| **S** | Restart measurements with new workers, preserving statistics (after **s**) |
| **R** | Reset statistics and UI logs; remain stopped if measurements are stopped |
| **a** | Open "Add host" dialog — type a hostname or IP and press Enter to add it at runtime |
| **d** | Confirm deletion of the selected target, or the target currently shown in details |
| **Tab** | Cycle focus: Ping Monitor → Traceroute Monitor → MTR Monitor → Port Monitor → HTTP Monitor → RTT Graphs → Log |
| **↑ / ↓ / PgUp / PgDn** | Select hosts in Ping Monitor; scroll other panes and host details |
| **Enter** | Open selected host details: statistics, routes, MTR, ports, graphs, and target events |
| **Esc** | Return from details, or restore the layout after maximizing a pane |
| **f** | Fold or expand the focused pane |
| **z** | Maximize the focused pane or restore its previous layout |
| **w** | Save a session report from the overview, or one target from details |

Deletion opens a confirmation showing the host, target ID, IP, and DSCP. **Cancel** (`キャンセル`) is selected initially; Enter cancels. Use Tab or the left/right arrows to select **Delete** (`削除`), then Enter to confirm. Esc cancels, and repeated `d` does not confirm. Measurements continue while the dialog is open. Cancelling restores the previous focus and scroll position. Re-adding a deleted target starts a new measurement. For the last remaining target, the dialog explains that **q** exits mping; **Enter** or **Esc** returns to monitoring.

Pane folding and maximization are available in the overview. Folded title rows remain reachable with Tab; press `f` to expand them. Measurements continue while panes are hidden. Tab stays within the maximized pane until it is restored.

> Live host edits preserve surviving targets' statistics, graphs, routes, port results, and independent HTTP checks. Editing a stopped session does not resume it. Re-adding a deleted host starts a new measurement. Existing `--count` budgets and the session's `--duration` deadline are preserved. Outstanding probes cancelled by deletion or stopping are exported as `cancelled`, not packet loss. YAML configuration reloads still reinitialize measurements and statistics.
>
> Selection and deletion use stable target IDs, including duplicate host names and DSCP variants. Echo IDs are never recycled within a session; after 32,768 worker allocations across initial starts, additions, and restarts, restart mping to obtain a fresh session.

Stop, restart and reset operations execute in keypress order. A running reset discards results from probes sent before the reset and recreates enabled route/port/HTTP monitors. It keeps the remaining `--count` budget; restarting starts a fresh budget. When stopped, reset clears ping and MTR counters while preserving the last route and port/HTTP results. Quitting waits for measurement workers before writing the final JSON snapshot.

### Saving investigation results

Press **w**, enter a new file path ending in **.txt** or **.json**, and press **Enter** or activate **Save**. The format is inferred from the extension and displayed below the input; no format selector is needed. A missing extension defaults to `.txt`; other extensions are rejected. **Tab** moves between fields and buttons; **Esc** cancels. Saving from the overview includes all active targets, retained final results of removed targets, and independent HTTP checks. Saving from host details includes only that target. Measurements continue during capture and writing; completion or errors appear in the footer and Log.

Reports include the session and collection start times, capture interval, stable target IDs, ping statistics window, effective settings, DNS IP changes, Ping, traceroute, MTR, port results, and retained target events. JSON reports use `schema_version: 1`; fields ending in `_ms` are milliseconds. `statistics` reuses the existing JSON target summary, including its `loss_rate_pct` denominator (sent probes) and separate cancelled probes.

The most recent 128 removed targets, 128 events per target, and 64 destination IP history entries are retained; reports include omitted-entry counts. YAML reload starts a new collection and clears earlier targets and removal history. Manual reset starts a new ping statistics window; when stopped, port/HTTP results retain their previous counters. Measurements and auxiliary checks are captured sequentially within the recorded capture interval. Reports contain aggregate results and retained events; use CSV output for ongoing individual ping records.

Reports are saved with owner-only read/write permissions (`0600`). When launched through sudo, ownership is assigned to the user who invoked sudo; setuid installations create the report as the invoking user. Ownership is set before publication; failure leaves no final report.

Existing files, including symlink destinations, are preserved. Each save writes a temporary file in the destination directory and atomically publishes the complete new file. Active CSV/JSON output paths are reserved for their existing writers. On a save error, choose a new writable path and retry with **w**.

## Shell completion

mping generates bash/zsh/fish completion scripts from the same flag definitions used by `mping --help`. Completion covers flag names, `-f`/`-o`/`-j` file paths, `-I` network interface names, and `mping completion bash|zsh|fish`. File and interface values also support `--file=...` / `--interface=...` and attached short options such as `-Ien`. Generating scripts and listing interfaces do not require `sudo`.

Use `mping completion --help` to show the supported shells. If running from a source checkout, build first with `make build` and use `./mping` in place of `mping` below.

### Bash

```bash
# Enable in the current shell; add this line to ~/.bashrc for future shells
source <(mping completion bash)
```

### Zsh

Enable in the current shell (run `compinit` first if your shell has not already initialized completion):

```zsh
autoload -Uz compinit
compinit
source <(mping completion zsh)
```

For persistent completion, save the script in a directory you own:

```zsh
mkdir -p ~/.zsh/completions
mping completion zsh > ~/.zsh/completions/_mping
```

Add the following to `~/.zshrc`, placing the `fpath` line before any existing `compinit` call (including one run by a shell framework), then start a new shell:

```zsh
fpath=(~/.zsh/completions $fpath)
autoload -Uz compinit
compinit
```

### Fish

```fish
mkdir -p ~/.config/fish/completions
mping completion fish > ~/.config/fish/completions/mping.fish
```

Regenerate saved scripts after upgrading mping to include new flags. To monitor a host literally named `completion`, use `mping -- completion`.

## TUI columns

* **Src IP** — Local IP address used for sending.
* **Dst IP** — Resolved destination IP. Shown as `domain (IP)` when a hostname is given.
* **ASN** — Autonomous System Number, country code, and organization name of the target IP (enabled with `-a`). Example: `AS15169 US Google LLC`.
* **Success** — Number of packets received successfully.
* **Loss** — Number of lost packets.
* **Loss Ratio** — Packet loss percentage. Colour boundaries are configurable (defaults shown).
  * **Green**: 0%–20% &nbsp;|&nbsp; **Orange**: 20%–80% &nbsp;|&nbsp; **Vivid red**: >80%
* **RTT / Avg / Jitter** — Latest / average / jitter round-trip time. Colour boundaries are configurable (defaults shown).
  * **RTT**: Green (≤50 ms) / Orange (≤200 ms) / Red (>200 ms)
  * **Jitter**: Green (≤10 ms) / Orange (≤50 ms) / Red (>50 ms)
  * Override with `--rtt-warn/--rtt-crit`, `--jitter-warn/--jitter-crit`, `--loss-warn/--loss-crit`, or the `thresholds:` YAML block.
* **Size** — Payload size of sent packets.
* **MTU** — MTU of the outbound interface.
* **TTL** — Time To Live of the last received packet.
* **Error** — Abbreviated latest error message (red). Full details appear in the Log pane.
* **Last Loss** — Time elapsed since the last packet loss.


## Traceroute Monitor pane

* Shown only when `-T` / `--traceroute` is given.
* Probes up to 30 hops and displays results in a Host / Route two-column table.
* Multiple targets are traced concurrently and separated by divider rows.
* One traceroute is run at startup, then automatically refreshed every 10 minutes.

## MTR Monitor pane

* Shown only when `-M` / `--mtr` is given.
* Performs continuous TTL-limited ICMP probing to every hop on the path to each target.
* The hop path is discovered at startup and re-discovered every 10 minutes to track route changes.
* Each hop is probed once per second. Unresponsive hops (`*`) accumulate 100% loss.
* The header row shows `SrcIP -> DstIP` (or `hostname (SrcIP -> DstIP)` for hostname targets).
* Can be combined with `-T` (both panes are displayed side by side).
* Columns:
  * **Hop** — TTL hop number
  * **Host** — Responder IP with ASN and country code when `-a` is given (e.g., `1.2.3.4 (AS15169 US)`). `*` when no response.
  * **Loss%** — Packet loss percentage for this hop (green / orange / red)
  * **Snt** — Total probes sent
  * **Recv** — Total replies received
  * **Last** — RTT of the most recent probe
  * **Avg** — Average RTT
  * **Min** — Minimum RTT
  * **Max** — Maximum RTT
  * **Jitter** — Smoothed inter-packet delay variation (RFC 1889)
* On narrow terminals, **Min**, **Max**, **Recv**, and **Jitter** columns are hidden automatically (compact mode).
* MTR statistics are included in the `-j` JSON export as `mtr_hops` per target.
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
* HTTP check results are included in the `-j` JSON export as `http_checks`.

## Port Monitor pane

* Shown only when `-p` / `--port` is given.
* Performs TCP/UDP reachability checks in real time at the ping interval.
* Multiple ports can be specified comma-separated (e.g. `-p 443/tcp,53/udp`).
* Omitting the protocol defaults to TCP (e.g. `-p 443` → `443/tcp`).
* Columns:
  * **Target** — Hostname
  * **Port** — Port number and protocol (e.g. `443/tcp`)
  * **Service** — Estimated service name (`Unknown` if not recognized)
  * **Status** — Green `Open` / Red `Closed` / Yellow `Filtered` or `Open|Filtered`
  * **Open/Closed** — Cumulative Open count / Closed+Filtered count
  * **Last Change** — Time elapsed since the last status change

## PMTU discovery

* Enabled with `--discovery-mtu` / `-m`.
* Probes maximum payload size using DF-bit ICMP, starting from 9872 bytes.
* The discovered size is reflected in the **Size** column.

## License

MIT
