# mping

**mping** は Go言語で書かれたターミナルベースのマルチターゲット Ping ツールです。複数のホストに対して同時に Ping を実行し、パケットロス率、RTT、TTL などの統計情報をリアルタイムで見やすい TUI (テキストユーザーインターフェース) で監視できます。

![Go Version1.24](https://img.shields.io/badge/go-v1.24-blue "Go Version1.24")![MIT License](https://img.shields.io/badge/license-MIT-blue "MIT License")[![Coverage Status](https://coveralls.io/repos/github/nagayon-935/mping/badge.svg?branch=main)](https://coveralls.io/github/nagayon-935/mping?branch=main)![Go Report Card](https://goreportcard.com/badge/github.com/nagayon-935/mping)

[English](./README.md)

## 特徴

* **複数ターゲットへの Ping**: 複数のホストを並行して監視できます。
* **リアルタイム統計**: パケットロス、RTT、TTL、エラーメッセージをリアルタイムで更新します。
* **TUI ダッシュボード**: 黒背景固定の視認性の高いテーブル表示を採用。ウィンドウ幅に応じて列幅を自動再配分し、狭い場合は 1 ターゲット 2 行のコンパクトレイアウトへ自動切替します。
* **色分けによる警告**: パケットロス率/RTT/Jitter に応じて直感的に状況を把握できます。閾値を超えると Log ペインにアラートが記録されます。
* **詳細な設定**: インターフェイス、ソースIP、パケットサイズ、送信回数などの柔軟な指定が可能です。
* **YAML ホストリスト**: ホスト一覧をファイルで管理できます。
* **Traceroute ペイン**: `-T` オプション指定時に Host/Route の 2 カラムテーブル形式で経路を表示します。複数ターゲットを同時に traceroute し、それぞれの結果を行で区切って一覧表示します。
* **Port Monitor ペイン**: `-p` オプション指定時に TCP/UDP ポートの疎通状況をリアルタイムで監視します。ポート番号から推定されるサービス名、Open/Closed の累計回数、最終ステータス変化時刻を表示します。
* **PMTU 探索**: DF 付きのパケットサイズ探索を実行できます。
* **自動ソースIP検出**: 指定がない場合でも、実際に通信に使用されているローカルIPを自動的に表示します。
* **RTT グラフ**: 各グラフは縦軸 0〜100ms、横軸 30 秒の固定レンジで表示されます。
* **CSV ログ出力**: 実行結果を統計情報とともにファイルに保存できます。

## 対応プラットフォーム

| OS | アーキテクチャ | 備考 |
| :--- | :--- | :--- |
| Linux | amd64, arm64 | `setcap` による `CAP_NET_RAW` 付与を推奨 |
| macOS | amd64, arm64 (Apple Silicon) | `setuid` を使用 |

> **権限について** — mping は正確な TTL を取得するために Raw ICMP ソケットを使用します。Linux では `setcap cap_net_raw+ep` での権限付与を推奨します (`install.sh` が自動で処理します)。macOS では `setuid` を使用します。

> **ターミナルの互換性** — Linux や macOS の標準ターミナルでは、色が正しく描画されないことがあります。色が正しく表示されない場合は、モダンなターミナルエミュレータ（iTerm2, Alacritty, kitty など）の使用を検討してください。

## インストール

### オプション 1 — リリース済みバイナリ (推奨)

[Releases](https://github.com/nagayon-935/mping/releases) ページから対応プラットフォームのアーカイブをダウンロードして展開し、同梱の `install.sh` を実行します。

#### Linux (amd64)

```bash
# ダウンロードと展開 (vX.Y.Z は最新バージョンに置き換えてください)
curl -LO https://github.com/nagayon-935/mping/releases/download/vX.Y.Z/mping-vX.Y.Z-linux-amd64.tar.gz
tar -xzf mping-vX.Y.Z-linux-amd64.tar.gz

# インストール (setcap で CAP_NET_RAW を付与。setcap が無い場合は setuid にフォールバック)
sudo ./install.sh
```

#### Linux (arm64 — Raspberry Pi, AWS Graviton など)

```bash
curl -LO https://github.com/nagayon-935/mping/releases/download/vX.Y.Z/mping-vX.Y.Z-linux-arm64.tar.gz
tar -xzf mping-vX.Y.Z-linux-arm64.tar.gz
sudo ./install.sh
```

#### macOS (Intel)

```bash
curl -LO https://github.com/nagayon-935/mping/releases/download/vX.Y.Z/mping-vX.Y.Z-darwin-amd64.tar.gz
tar -xzf mping-vX.Y.Z-darwin-amd64.tar.gz
sudo ./install.sh
```

#### macOS (Apple Silicon)

```bash
curl -LO https://github.com/nagayon-935/mping/releases/download/vX.Y.Z/mping-vX.Y.Z-darwin-arm64.tar.gz
tar -xzf mping-vX.Y.Z-darwin-arm64.tar.gz
sudo ./install.sh
```

`install.sh` はバイナリを `INSTALL_DIR` (デフォルト: `/usr/local/bin`) にコピーし、OS に応じて権限を設定します:

* **Linux**: `setcap cap_net_raw+ep` を付与 (未インストールの場合は setuid にフォールバック)
* **macOS**: `chown root` + `chmod u+s` で setuid を付与

インストール先を変更する場合:

```bash
sudo INSTALL_DIR=/usr/local/bin ./install.sh
```

インストール後は `sudo` なしで実行できます:

```bash
mping google.com 1.1.1.1
```

---

### オプション 2 — ソースコードからビルド

**必須要件:** Go 1.24 以上

```bash
git clone https://github.com/nagayon-935/mping.git
cd mping
```

#### make を使ったビルド

```bash
# ビルドのみ
make build

# ビルド + インストール (macOS では setuid を付与。Linux では setcap のために install.sh を使用することを推奨)
make install
```

> **Linux ユーザーへ:** `make install` は setuid を設定しますが、これは動作しますが setcap より安全性が低くなります。本番環境では `go build -o mping ./cmd/main` の後に `sudo ./install.sh` を実行して `setcap` で `CAP_NET_RAW` を付与することを推奨します。

#### go build を使ったビルド

```bash
go build -o mping ./cmd/main
sudo ./install.sh
```

## 使い方

```bash
# 基本的な使い方 (install.sh 実行後は sudo 不要)
mping google.com 1.1.1.1 8.8.8.8

# インターフェイスを指定して実行
mping -I eth0 google.com

# パケットサイズ (100バイト) と送信回数 (10回) を指定
mping -s 100 -c 10 google.com

# ログを CSV ファイルに出力
mping -o results.csv google.com

# YAML からホストを読み込み
mping -f hosts.yaml

# IPv4 のみを強制
mping -4 google.com

# IPv6 のみを強制
mping -6 google.com

# Traceroute ペインを表示
mping -T google.com

# PMTU 探索 (payload 上限 9872 から探索)
mping -m google.com

# ポート疎通確認 (443/tcp)
mping -p 443/tcp google.com

# 複数ポートをカンマ区切りで指定
mping -p 443/tcp,53/udp google.com 8.8.8.8

# Traceroute と Port Monitor を同時に表示
mping -T -p 443/tcp google.com
```

> インストールせずに実行する場合 (`setcap`/`setuid` なし) は `sudo` を付けてください:
> ```bash
> sudo ./mping google.com
> ```

### hosts.yaml の例

`hosts:` キーでホストを列挙します。CLI で明示的に指定したオプションは YAML の値より優先されます。

```yaml
hosts:
  - google.com
  - 1.1.1.1
interval: 500
timeout: 2000
traceroute: true
port:
  - 443/tcp
  - 53/udp
```

### オプション

| フラグ | 短縮形 | 説明 | デフォルト |
| :--- | :--- | :--- | :--- |
| `--interval` | `-i` | Ping の送信間隔 (ミリ秒) | `1000` |
| `--timeout` | `-t` | Ping のタイムアウト (ミリ秒) | `1000` |
| `--file` | `-f` | ホスト一覧の YAML ファイルパス | `""` |
| `--traceroute` | `-T` | Traceroute ペインを表示する | `false` |
| `--discovery-mtu` | `-m` | 最大 payload サイズを DF で探索する | `false` |
| `--interface` | `-I` | 使用するネットワークインターフェイス名 (例: `eth0`, `en0`) | `""` |
| `--source` | `-S` | 送信元 IPv4 アドレスの指定 | `""` (自動検出) |
| `--size` | `-s` | パケットのペイロードサイズ (バイト) | `56` |
| `--count` | `-c` | 各ターゲットに送信する回数 (0 は無制限) | `0` |
| `--ipv4` | `-4` | IPv4 のみを使用する | `false` |
| `--ipv6` | `-6` | IPv6 のみを使用する | `false` |
| `--output` | `-o` | CSV 形式でのログ出力ファイルパス | `""` |
| `--port` | `-p` | 疎通確認するポート (例: `443/tcp`, `53/udp`, `443`)。カンマ区切りで複数指定可 | `""` |

### キー操作

| キー | 動作 |
| :--- | :--- |
| **q** | アプリケーションを終了する |
| **s** | Ping 送信を一時停止する |
| **S** | Ping 送信を再開する (**s** で一時停止した後のみ有効) |
| **R** | 全ての統計情報とログをリセットする |
| **Tab** | フォーカスを切り替える: Ping Monitor → Traceroute Monitor → Port Monitor → RTT Graphs → Log |
| **↑ / ↓ / PgUp / PgDn** | フォーカス中のペインをスクロール (Table / Traceroute / RTT Graphs) |

## 表示項目 (TUI カラム)

* **Src IP**: 送信に使用されているローカル IP アドレス。
* **Dst IP**: 名前解決された宛先 IP アドレス。ドメイン名で指定した場合は `domain (IP)` の形式で表示。
* **Success**: 受信に成功したパケット数。
* **Loss**: 損失したパケット数。
* **Loss Ratio**: パケット損失率。
  * **緑**: 0%〜20% &nbsp;|&nbsp; **オレンジ**: 20%〜80% &nbsp;|&nbsp; **鮮やかな赤**: >80%
* **RTT / Avg / Jitter**: 往復時間 (Round Trip Time) の最新/平均/ジッタ値。
  * **RTT**: 緑 (≤50ms) / オレンジ (≤200ms) / 赤 (>200ms)
  * **Jitter**: 緑 (≤10ms) / オレンジ (≤50ms) / 赤 (>50ms)
* **Size**: 送信パケットのペイロードサイズ。
* **MTU**: 送信に使用されているインターフェイスの MTU。
* **TTL**: 最後のパケットの生存時間 (Time To Live)。
* **Error**: 最新エラーの短縮メッセージを表示 (赤色)。詳細は Log ペインに表示されます。
* **Last Loss**: 最後にパケットロスが発生してからの経過時間。

## Traceroute Monitor ペイン

* `-T` / `--traceroute` 指定時のみ表示されます。
* 最大 30 ホップまで探索し、Host と Route の 2 カラム形式で結果を表示します。
* 複数ターゲット指定時は行で区切って一覧表示します。
* 起動後に一度 traceroute を実行し、その後 10 分ごとに自動更新します。

## Port Monitor ペイン

* `-p` / `--port` 指定時のみ表示されます。
* 指定したポートへの TCP/UDP 疎通確認を Ping と同じ間隔でリアルタイムに実行します。
* カンマ区切りで複数のポートを一度に指定できます (例: `-p 443/tcp,53/udp`)。
* プロトコルを省略した場合は TCP とみなします (例: `-p 443` → `443/tcp`)。
* 表示カラム:
  * **Target**: 対象ホスト名
  * **Port**: ポート番号とプロトコル (例: `443/tcp`)
  * **Service**: ポート番号から推定されるサービス名 (不明な場合は `Unknown`)
  * **Status**: 疎通結果 — 緑 `Open` / 赤 `Closed` / 黄 `Filtered` または `Open|Filtered`
  * **Open/Closed**: Open 判定回数 / Closed・Filtered 判定回数の累計
  * **Last Change**: ステータスが最後に変化してからの経過時間

## PMTU 探索

* `--discovery-mtu` / `-m` 指定時に最大 payload サイズを探索します。
* DF 付き ICMP を使い、payload 上限は 9872 バイトから開始します。
* 探索結果は **Size** カラムに反映されます。

## ライセンス

MIT
