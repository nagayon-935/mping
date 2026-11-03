# README screenshots

How the images in the top-level READMEs are made. Re-take them after visible UI changes.
Run everything from the repository root with an installed (privileged) mping.

| Image | Source |
| :--- | :--- |
| `assets/mping-tui.gif` | `tui.tape` ([VHS](https://github.com/charmbracelet/vhs)) |
| `assets/mping-web-light.png`, `assets/mping-web-dark.png` | `web-shot.mjs` (Node 22+, Chrome) |

Both use [`hosts.yaml`](hosts.yaml), which lists public hosts only. Keep `-T` / `-M` out of
these captures: the route panes would show the hops of your own network.

## TUI

```bash
vhs assets/demo/tui.tape
```

The tape starts mping hidden, waits for the statistics and graphs to fill, then records
10 seconds. If its `Wait` times out, the TUI did not come up in VHS's terminal; run it again.

## Web UI

```bash
mping -f assets/demo/hosts.yaml --web
```

Leave it running for about five minutes so the 5-minute RTT graphs are full, then in
another terminal:

```bash
node assets/demo/web-shot.mjs "/path/to/Google Chrome"   # or chrome-headless-shell
```

It opens `http://127.0.0.1:8080/` read-only at 1440×900 (2×), selects
`fra-de-ping.vultr.com` so its Summary is shown, and writes the light and dark images.
