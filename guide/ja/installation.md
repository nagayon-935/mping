# インストール

← [README](../../README.ja.md)

## インストール

### リリース済みバイナリ (推奨)

Linux/macOS では、[Releases](https://github.com/nagayon-935/mping/releases) ページから対応プラットフォームのアーカイブをダウンロードして展開し、同梱の `install.sh` を実行します。Windows のアーカイブには `mping.exe` が含まれるため、管理者のシェルから実行してください。

このガイドは現在のソースツリーの機能を説明しています。リリース済みバイナリはバージョンによって機能が異なるため、記載内容と同じ実装を使う場合は、このソースツリーからビルドしてください。以下の `v0.7.2` はアーカイブ名の例なので、インストールするリリースのバージョンに置き換えてください。

#### Linux (amd64)

```bash
# ダウンロードと展開 (v0.7.2 は例です。最新バージョンは Releases ページで確認してください)
curl -LO https://github.com/nagayon-935/mping/releases/download/v0.7.2/mping-v0.7.2-linux-amd64.tar.gz
tar -xzf mping-v0.7.2-linux-amd64.tar.gz

# インストール (setcap で CAP_NET_RAW を付与。setcap が無い場合は setuid にフォールバック)
sudo ./install.sh
```

#### Linux (arm64 — Raspberry Pi, AWS Graviton など)

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

`install.sh` はバイナリを `INSTALL_DIR` (デフォルト: `/usr/local/bin`) にコピーし、OS に応じて権限を設定します:

* **Linux**: `setcap cap_net_raw+ep` を付与 (未インストールの場合は setuid にフォールバック)
* **macOS**: `chown root` + `chmod u+s` で setuid を付与

インストール先を変更する場合:

```bash
sudo mkdir -p /opt/mping/bin
sudo INSTALL_DIR=/opt/mping/bin ./install.sh
export PATH="/opt/mping/bin:$PATH"
```

インストール後は `sudo` なしで実行できます:

```bash
mping google.com 1.1.1.1
```

### ソースコードからビルド

**必須要件:** Go 1.26.11 以上

```bash
git clone https://github.com/nagayon-935/mping.git
cd mping
```

#### make を使ったビルド

```bash
# ビルドのみ
make build

# ビルド + インストール (macOS は setuid、Linux は利用可能なら setcap)
make install
```

`make install` はバイナリを再ビルドし、`sudo ./install.sh` を実行します。デフォルトの配置先は `/usr/local/bin/mping` で、上記のOS別の権限を設定します。PATH内で別の `mping` が先に見つかる場合は、そのバイナリが起動するため、`type -a mping` で確認してください。古いバイナリを移動・削除した後は、`hash -r` でシェルのコマンドキャッシュを消します。

#### go build を使ったビルド

```bash
go build -o mping ./cmd/main
sudo ./install.sh
```
