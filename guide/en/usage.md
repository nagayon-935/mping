# Usage and options

← [README](../../README.md)

Without `-4` / `-6`, hostnames use the address order and route/source selection of the OS and Go dialer. Each destination selects an IPv4 or IPv6 address that can be sent to locally; there is no fixed IPv4 preference or global IPv6-connectivity check against an unrelated host. This uses a UDP socket connection without sending datagrams. It checks local routing, not whether the destination answers Ping. Subsequent Ping loss does not trigger a family switch. DNS re-resolution re-evaluates the choice.

Literal IPs retain their specified version and IPv6 zone. `-S` restricts selection to that source's family; `-I` alone leaves both families available on that interface. On Windows/FreeBSD, `-I` binds the interface’s per-family source addresses instead of using native interface binding. `-4` / `-6` remain explicit overrides. `--resolve-all` deliberately monitors all DNS addresses in the requested family, including currently unroutable ones.

These family choices apply to Ping targets and their route/port checks. HTTP URLs are checked independently; `-4` / `-6` alone do not force the HTTP address family. Source/interface binding (`-S` / `-I`) also applies to HTTP connections.

`-c` finishes ICMP measurement after the count but leaves the TUI open. Use `--duration 30s` to exit automatically after a time limit, or **q** to quit.

```bash
# Basic (no sudo needed after install.sh)
mping google.com 1.1.1.1 8.8.8.8

# Specify network interface
mping -I eth0 google.com

# Set packet size (100 bytes) and count (10 packets)
mping -s 100 -c 10 google.com

# Exit automatically after 30 seconds
mping --duration 30s google.com

# Load hosts from a YAML file
mping -f hosts.yaml

# Force IPv4 only
mping -4 google.com

# Force IPv6 only
mping -6 google.com

# Show Traceroute pane
mping -T google.com

# PMTU discovery (automatically uses the selected address's version)
mping -m google.com

# IPv6 PMTU is selected from the destination IP; the same -m works for both
mping -m 1.1.1.1 2606:4700:4700::1111

# TCP port reachability check (443/tcp)
mping -p 443/tcp google.com

# Multiple ports (comma-separated)
mping -p 443/tcp,53/udp google.com 8.8.8.8

# MTR-style per-hop monitor
mping -M google.com

# HTTP(S) health check
mping -H https://example.com/health google.com

# Multiple HTTP endpoints
mping -H https://api.example.com/health,https://cdn.example.com/ping google.com

# Traceroute + Port Monitor simultaneously
mping -T -p 443/tcp google.com

# Customise colour-coding thresholds (warn = orange, crit = red)
mping --rtt-warn 30 --rtt-crit 100 --loss-warn 10 --loss-crit 50 google.com

# Display AS numbers for target IPs
mping -a google.com 1.1.1.1
```

> If you run mping **without** installing (i.e. without `setcap`/`setuid`), prepend `sudo`:
> ```bash
> sudo ./mping google.com
> ```

## Options

| Flag | Short | Description | Default |
| :--- | :--- | :--- | :--- |
| `--interval` | `-i` | Ping send interval, 100–60000 ms | `1000` |
| `--timeout` | `-t` | Ping timeout, 10–30000 ms | `1000` |
| `--file` | `-f` | Path to a YAML hosts file or a NetBox IP Addresses CSV export | `""` |
| `--traceroute` | `-T` | Show each target's route, rediscovered every 10 minutes (not with `-M`) | `false` |
| `--mtr` | `-M` | Also probe every hop each second, MTR-style (not with `-T`) | `false` |
| `--dns-server` | `-d` | Custom DNS server IP address to resolve target hostnames | `""` (OS default) |
| `--resolve-all` | | Resolve target hostnames to all IP addresses and monitor them concurrently | `false` |
| `--discovery-mtu` | `-m` | Discover PMTU using IPv4 DF probes or IPv6 OS estimates with echo confirmation | `false` |
| `--interface` | `-I` | Network interface name (e.g. `eth0`, `en0`); selects a source per destination family | `""` |
| `--source` | `-S` | Source IP address, including scoped IPv6 | `""` (auto-detect) |
| `--size` | `-s` | Payload size in bytes | `56` |
| `--count` | `-c` | Number of packets per target (0 = unlimited) | `0` |
| `--duration` | | Exit after the specified duration, e.g. `30s`, `5m`, `1h30m` (0 = no limit) | `0s` |
| `--ipv4` | `-4` | Use IPv4 only | `false` |
| `--ipv6` | `-6` | Use IPv6 only | `false` |
| `--port` | `-p` | Ports to check (e.g. `443/tcp`, `53/udp`, `443`). Comma-separated for multiple. | `""` |
| `--web` | | Serve the web UI on `http://127.0.0.1:<web-port>/` (this machine only; see [Web UI](web-ui.md)) | `false` |
| `--web-port` | | Port for `--web` (1–65535) | `8080` |
| `--asn` | `-a` | Look up and display AS numbers for target IPs | `false` |
| `--ptr` | `-r` | Look up and display reverse DNS names for target and hop IPs | `false` |
| `--http` | `-H` | URL(s) to health-check, e.g. `https://example.com/health`. Comma-separated or repeated for multiple. | `""` |
| `--check-config` | | Validate and print effective configuration without starting monitoring (CLI only) | `false` |
| `--rtt-warn` | | RTT warn threshold in ms (orange) | `50` |
| `--rtt-crit` | | RTT crit threshold in ms (red) | `200` |
| `--jitter-warn` | | Jitter warn threshold in ms (orange) | `10` |
| `--jitter-crit` | | Jitter crit threshold in ms (red) | `50` |
| `--loss-warn` | | Loss warn threshold in percent (orange) | `20` |
| `--loss-crit` | | Loss crit threshold in percent (red) | `80` |

> **Thresholds** — `warn` is the orange boundary and `crit` the red boundary for colour-coding the LOSS%, RTT, and JIT columns (and for triggering alert log entries). For each metric `warn` must be less than `crit`. These can also be set in the `thresholds:` block of the YAML file.

## Current health and configuration checks

Current health is separate from lifetime loss and RTT statistics. By default, **3 consecutive failures** confirm an unavailable target and **2 consecutive replies** clear that condition. Recent loss, latest RTT and jitter can still warn or remain critical during recovery; historical average/peak RTT never drive current health. Loss thresholds require at least `health-failures` completed probes in the recent window, avoiding an immediate outage on the first startup timeout. The table's Loss % remains the lifetime rate over completed probes.

| Option (also a top-level YAML key) | Default | Meaning |
|---|---|---|
| `--health-failures` | `3` | Consecutive failures to confirm unavailability (1–10000) |
| `--health-recoveries` | `2` | Consecutive replies to clear unavailability (1–10000) |
| `--health-window` | `1m` | Recent-loss window, 1s–1h, in one-second buckets; changing its length starts a fresh recent window |
| `--http-follow-redirects` | `true` | Follow redirects, up to 10; false inspects the first response |

`health` in snapshots/reports includes status, reason, unavailability, consecutive counts, recent loss, and last probe/reply timestamps. Recent counters describe the window at `evaluated_at` (the last completed probe or policy change), so stopping preserves the judgment and its inputs together. `measurement_state` (`running`, `finished`, `stopped`) describes ICMP measurement separately from the Web server's `state`; after `--count` completes, auxiliary monitors may continue in the TUI. Web RTT sparklines and the Summary chart use actual timestamps and leave pauses blank instead of shifting an old reply to now.

Each target in snapshots/reports also carries `levels` (`loss`, `last_rtt`, `jitter`), each `none`, `ok`, `warn` or `crit` against the configured thresholds; MTR hops carry `loss_level`, and port and HTTP checks `level` (`pending` before the first result). These are the values the TUI and Web UI colour by.

```bash
mping --check-config -f examples/monitoring.yaml --interval 500
```

`--check-config` (CLI only) prints the effective settings, expanded targets/count, groups and explicit CLI overrides. It starts no probes, listeners, UI or output writers and needs no raw-socket privileges. `--resolve-all` may perform DNS lookups. CLI flags take precedence over YAML.

HTTP checks count a 2xx or 3xx response as up and anything else as down. URLs must be HTTP(S) without embedded credentials or fragments.

Backend health transitions reach the TUI and Web logs. Consecutive identical ping failures are folded into one event with `count` and `last_at`. Web settings and the session duration deadline remain startup-only; changes log a restart notice. Port and network changes rebuild the relevant measurement session. See [examples/monitoring.yaml](../../examples/monitoring.yaml).

## Shell completion

mping generates bash/zsh/fish completion scripts from the same flag definitions used by `mping --help`. Completion covers flag names, `-f` file paths, `-I` network interface names, and `mping completion bash|zsh|fish`. File and interface values also support `--file=...` / `--interface=...` and attached short options such as `-Ien`. Generating scripts and listing interfaces do not require `sudo`.

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
