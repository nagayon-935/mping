# Installation

← [README](../../README.md)

## Installation

### Pre-built binary (recommended)

For Linux/macOS, download the archive for your platform from the [Releases](https://github.com/nagayon-935/mping/releases) page and run the bundled `install.sh`. Windows archives contain `mping.exe`; run it from an Administrator shell.

These guides describe the current source tree. Release binaries can differ by version; build from this source tree to use the functionality described here. The `v0.7.2` download commands below illustrate archive naming, so replace that version with the release you intend to install.

#### Linux (amd64)

```bash
# Download and extract (v0.7.2 shown; check the Releases page for the latest version)
curl -LO https://github.com/nagayon-935/mping/releases/download/v0.7.2/mping-v0.7.2-linux-amd64.tar.gz
tar -xzf mping-v0.7.2-linux-amd64.tar.gz

# Install (grants CAP_NET_RAW via setcap; falls back to setuid if setcap is unavailable)
sudo ./install.sh
```

#### Linux (arm64 — e.g. Raspberry Pi, AWS Graviton)

```bash
curl -LO https://github.com/nagayon-935/mping/releases/download/v0.7.2/mping-v0.7.2-linux-arm64.tar.gz
tar -xzf mping-v0.7.2-linux-arm64.tar.gz
sudo ./install.sh
```

#### macOS (Intel)

```bash
curl -LO https://github.com/nagayon-935/mping/releases/download/v0.7.2/mping-v0.7.2-darwin-amd64.tar.gz
tar -xzf mping-v0.7.2-darwin-amd64.tar.gz
sudo ./install.sh
```

#### macOS (Apple Silicon)

```bash
curl -LO https://github.com/nagayon-935/mping/releases/download/v0.7.2/mping-v0.7.2-darwin-arm64.tar.gz
tar -xzf mping-v0.7.2-darwin-arm64.tar.gz
sudo ./install.sh
```

`install.sh` copies the binary to `INSTALL_DIR` (default: `/usr/local/bin`) and sets the appropriate privilege:

* **Linux** — `setcap cap_net_raw+ep` (falls back to `setuid` if `setcap` is not available)
* **macOS** — `chown root` + `chmod u+s` (setuid)

To install to a different directory:

```bash
sudo mkdir -p /opt/mping/bin
sudo INSTALL_DIR=/opt/mping/bin ./install.sh
export PATH="/opt/mping/bin:$PATH"
```

After installation, run mping **without** `sudo`:

```bash
mping google.com 1.1.1.1
```

### Build from source

**Requirements:** Go 1.26.11 or later

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
