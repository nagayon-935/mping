# Hosts file

← [README](../../README.md)

## hosts.yaml example

List hosts under the `hosts:` key. Options specified here are overridden by explicit CLI flags.

```yaml
hosts:
  - google.com
  - 1.1.1.1
interval: 500
timeout: 2000
mtr: true
asn: true
port:
  - 443/tcp
  - 53/udp
dns-server: 8.8.8.8
resolve-all: true
web: true
web-port: 8080
thresholds:
  rtt-warn: 50      # ms (orange)
  rtt-crit: 200     # ms (red)
  jitter-warn: 10   # ms (orange)
  jitter-crit: 50   # ms (red)
  loss-warn: 20     # percent (orange)
  loss-crit: 80     # percent (red)
```

## Host groups in YAML

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

See [examples/hosts-groups.yaml](../../examples/hosts-groups.yaml) for a runnable example (`mping -f examples/hosts-groups.yaml`).

## Adding many hosts: patterns, names, and include files

Host entries (in `hosts:` and `groups[].hosts:`) may be patterns that expand to several hosts:

| Pattern | Expands to |
|---|---|
| `10.0.0.1-20` | `10.0.0.1` … `10.0.0.20` (last octet) |
| `10.0.0.250-10.0.1.5` | every IPv4 address in the range |
| `192.0.2.0/28` | `192.0.2.1` … `192.0.2.14` (IPv4 /30 and wider skip the network and broadcast addresses; IPv6 CIDRs keep every address) |
| `core-sw{01..12}.lab` | `core-sw01.lab` … `core-sw12.lab` (zero padding is kept; several `{N..M}` form a product) |

A single pattern may expand to at most 1024 hosts and a whole hosts file (including include files) to at most 4096 targets. IPv6 ranges are not supported — use CIDR notation.

Give a host a display `name` to show it instead of the address (the IP is still shown next to it). Names must be unique and must not equal another entry's host; a name cannot be used on a pattern that expands to several hosts. The name also replaces the host in reports (the resolved IP is still recorded).

```yaml
hosts:
  - {host: 10.0.0.1, name: core-sw01}
```

`include:` (top level or per group) appends the hosts from NetBox IP Addresses CSV exports (CSVs with an `IP Address` column), resolved relative to the YAML file; a single export can also be passed directly via `mping -f netbox.csv`. Plain host lists (one host per line, or `host,name` CSV files) are not supported and are rejected — list those hosts in the YAML file. Prefix lengths (such as `/23`) are stripped automatically, only rows whose `Status` is `Active` (or blank) are loaded, and display names prefer `DNS name` (falling back to `Parent`), automatically disambiguating duplicates with the `Interface` and/or IP address. The file must be readable by the user running mping. Include files are watched for changes like the YAML file itself, including ones added to the YAML before they exist.

```yaml
groups:
  - name: Core
    hosts: ["core-sw{01..04}.lab"]
  - name: Branches
    include: [tokyo-netbox.csv, osaka-netbox.csv]
```

See [examples/hosts-patterns.yaml](../../examples/hosts-patterns.yaml) and [examples/branches.csv](../../examples/branches.csv).
