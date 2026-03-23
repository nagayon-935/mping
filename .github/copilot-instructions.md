# Copilot Code Review Instructions

## General

- This project is a multi-target terminal ping monitor written in Go 1.24.
- Review with correctness, thread safety, testability, and TUI responsiveness in mind.

## Concurrency

- All shared state must be protected by a mutex or accessed via channels.
- `TargetStats` fields must not be read or written without holding the appropriate lock.
- Use the snapshot pattern (`GetView()`) when passing data to the UI layer — never pass mutable state directly.
- Goroutines must have a clear termination path using `done chan struct{}` and `sync.WaitGroup`.

## Error Handling

- Do not swallow errors silently; log or propagate them appropriately.
- Network errors (ICMP, TCP, UDP) should be categorized and stored in `TargetStats.LastErr`.
- Avoid `panic` in library code; return errors instead.

## Interfaces and Testability

- New components that perform I/O (network, time, file system) must be abstracted behind an interface or injected via an `Options` struct.
- Tests must not require real network access; use function injection (`resolveIPAddrFunc`, `listenPacketFunc`, etc.).

## Statistics and Calculations

- Jitter must follow RFC 1889: `J = J + (|D| - J) / 16`.
- RTT history is a ring buffer of size 3000; do not grow it dynamically.
- Use `time.Duration` for all RTT, jitter, and timeout values — not raw integers.

## TUI (tview/tcell)

- UI rendering must only read `TargetView` (immutable snapshots), never `TargetStats` directly.
- Layout changes must handle both full and compact modes; test at narrow terminal widths.
- Color thresholds:
  - Loss: green < 20%, orange < 80%, red ≥ 80%
  - RTT: green ≤ 50ms, orange ≤ 200ms, red > 200ms
  - Jitter: green ≤ 10ms, orange ≤ 50ms, red > 50ms
- Do not block the tview event loop; heavy computation must run in a separate goroutine.

## Configuration

- CLI flags take precedence over YAML file values. Use `fs.Changed()` to detect explicit CLI flags.
- YAML config is restricted to the `hosts:` mapping format only.

## Style

- Follow standard Go conventions (`gofmt`, `go vet`).
- Keep functions focused and under ~80 lines where possible.
- Prefer table-driven tests with descriptive `name` fields.
