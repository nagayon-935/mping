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
* **ホストグループ**: YAML ファイルで名前付きグループを定義できます。グループごとにホスト数つきのヘッダ行を表示し、ターゲットを視覚的にまとめて確認できます。
* **Traceroute ペイン**: `-T` オプション指定時に Host/Route の 2 カラムテーブル形式で経路を表示します。複数ターゲットを同時に traceroute し、それぞれの結果を行で区切って一覧表示します。
* **Paris Traceroute**: `--paris` / `-P` オプションで有効化。ICMP のフロー識別子（ID と Sequence）を全 TTL プローブ間で固定することで、ECMP ロードバランサ環境でも全ホップが同一経路を通ることを保証します。ファントムホップ（実際には経路上にない IP がトレース結果に現れる現象）を排除し、正確な経路を表示できます。`-T` を自動で有効化します。
* **MTR Monitor ペイン**: `-M` オプション指定時に、経路上の各ホップに対してリアルタイムでロス率/レイテンシ統計を計測・表示します。`mtr` コマンドと同様の Hop / Host / Loss% / Snt / Recv / Last / Avg / Min / Max / Jitter カラムを表示。`-T` と同時使用可能です。
* **HTTP(S) ヘルスチェックペイン**: `-H` オプション指定時に HTTP/HTTPS エンドポイントへ GET リクエストを送信し、ステータスコードとレスポンスタイム (Last/Min/Avg/Max) および Up/Down 累計回数をリアルタイムで監視します。
* **Port Monitor ペイン**: `-p` オプション指定時に TCP/UDP ポートの疎通状況をリアルタイムで監視します。ポート番号から推定されるサービス名、**Last / Min / Avg / Max RTT**（TCP 接続確立または UDP 応答までの往復時間）、Open/Closed の累計回数、最終ステータス変化時刻を表示します。RTT 統計は `Open` 応答時のみ記録されます。
* **PMTU 探索**: DF 付きのパケットサイズ探索を実行できます。
* **自動ソースIP検出**: 指定がない場合でも、実際に通信に使用されているローカルIPを自動的に表示します。
* **RTT グラフ**: 縦軸は自動スケール（スパイク時に即拡大、一定期間後に縮小）、横軸 30 秒。ICMP と TCP/UDP ポートの両系列を表示できます。
* **CSV ログ出力**: 実行結果を統計情報とともにファイルに保存できます。
* **JSON 統計エクスポート**: `-j` オプションで 5 秒ごとに全統計情報の JSON スナップショットをファイルへ出力します。

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

# MTR 風の経路別ロス/レイテンシ監視
mping -M google.com

# MTR + Traceroute を同時に表示
mping -T -M google.com

# Traceroute と Port Monitor を同時に表示
mping -T -p 443/tcp google.com

# 統計情報を JSON ファイルへリアルタイム出力 (5 秒ごとに更新)
mping -j stats.json google.com 1.1.1.1

# 色分けの閾値をカスタマイズする (warn = オレンジ, crit = 赤)
mping --rtt-warn 30 --rtt-crit 100 --loss-warn 10 --loss-crit 50 google.com

# ターゲット IP の AS 番号を表示する
mping -a google.com 1.1.1.1

# Paris Traceroute (ECMP 経路を固定して正確なホップを表示)
mping -P google.com

# Paris Traceroute + MTR を同時に表示
mping -P -M google.com
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
mtr: true
asn: true
port:
  - 443/tcp
  - 53/udp
json-output: stats.json
thresholds:
  rtt-warn: 50      # ミリ秒 (オレンジ)
  rtt-crit: 200     # ミリ秒 (赤)
  jitter-warn: 10   # ミリ秒 (オレンジ)
  jitter-crit: 50   # ミリ秒 (赤)
  loss-warn: 20     # パーセント (オレンジ)
  loss-crit: 80     # パーセント (赤)
```

### YAML でのホストグループ定義

`groups:` キーで名前付きグループを定義できます。グループはヘッダ行と最悪値集計行つきで表示されます。`hosts:` に列挙したホストと共存でき、グループ外ホストは表の上部に表示されます。

```yaml
hosts:
  - 8.8.8.8        # グループ外 — 全グループより上に表示

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


### オプション

| フラグ | 短縮形 | 説明 | デフォルト |
| :--- | :--- | :--- | :--- |
| `--interval` | `-i` | Ping の送信間隔 (ミリ秒) | `1000` |
| `--timeout` | `-t` | Ping のタイムアウト (ミリ秒) | `1000` |
| `--file` | `-f` | ホスト一覧の YAML ファイルパス | `""` |
| `--traceroute` | `-T` | Traceroute ペインを表示する | `false` |
| `--paris` | `-P` | Paris Traceroute アルゴリズムを使用する（ECMP 経路を固定）。`-T` を自動で有効化 | `false` |
| `--mtr` | `-M` | MTR Monitor ペインを表示する (経路別ロス/レイテンシの継続計測) | `false` |
| `--discovery-mtu` | `-m` | 最大 payload サイズを DF で探索する | `false` |
| `--interface` | `-I` | 使用するネットワークインターフェイス名 (例: `eth0`, `en0`) | `""` |
| `--source` | `-S` | 送信元 IPv4 アドレスの指定 | `""` (自動検出) |
| `--size` | `-s` | パケットのペイロードサイズ (バイト) | `56` |
| `--count` | `-c` | 各ターゲットに送信する回数 (0 は無制限) | `0` |
| `--ipv4` | `-4` | IPv4 のみを使用する | `false` |
| `--ipv6` | `-6` | IPv6 のみを使用する | `false` |
| `--output` | `-o` | CSV 形式でのログ出力ファイルパス | `""` |
| `--port` | `-p` | 疎通確認するポート (例: `443/tcp`, `53/udp`, `443`)。カンマ区切りで複数指定可 | `""` |
| `--json-output` | `-j` | 統計情報の JSON スナップショットを出力するファイルパス (5 秒ごとに更新) | `""` |
| `--asn` | `-a` | ターゲット IP の AS 番号を検索して表示する | `false` |
| `--rtt-warn` | | RTT の warn 閾値 (ミリ秒・オレンジ) | `50` |
| `--rtt-crit` | | RTT の crit 閾値 (ミリ秒・赤) | `200` |
| `--jitter-warn` | | Jitter の warn 閾値 (ミリ秒・オレンジ) | `10` |
| `--jitter-crit` | | Jitter の crit 閾値 (ミリ秒・赤) | `50` |
| `--loss-warn` | | ロス率の warn 閾値 (パーセント・オレンジ) | `20` |
| `--loss-crit` | | ロス率の crit 閾値 (パーセント・赤) | `80` |

> **閾値 (Thresholds)** — `warn` がオレンジ、`crit` が赤の境界値で、Loss Ratio / RTT / Jitter カラムの色分け (および Log ペインのアラート記録) に使われます。各メトリクスで `warn` は `crit` より小さい必要があります。YAML の `thresholds:` ブロックでも設定できます。

### キー操作

| キー | 動作 |
| :--- | :--- |
| **q** | アプリケーションを終了する |
| **s** | Ping 送信を一時停止する |
| **S** | Ping 送信を再開する (**s** で一時停止した後のみ有効) |
| **R** | 全ての統計情報とログをリセットする |
| **a** | 「ホスト追加」ダイアログを開く — ホスト名または IP を入力して Enter で実行時に追加 |
| **d** | 「ホスト削除」ダイアログを開く — ↑/↓ でホストを選択して Enter で実行時に削除 |
| **Tab** | フォーカスを切り替える: Ping Monitor → Traceroute Monitor → MTR Monitor → Port Monitor → RTT Graphs → Log |
| **↑ / ↓ / PgUp / PgDn** | フォーカス中のペインをスクロール (Table / Traceroute / RTT Graphs) |

> **注意:** ホストの追加・削除を行うと、全ターゲットの統計情報がリセットされます (YAML 設定リロードと同等の動作)。

## 表示項目 (TUI カラム)

* **Src IP**: 送信に使用されているローカル IP アドレス。
* **Dst IP**: 名前解決された宛先 IP アドレス。ドメイン名で指定した場合は `domain (IP)` の形式で表示。
* **ASN**: ターゲット IP の AS 番号・国コード・組織名 (`-a` 指定時のみ表示)。例: `AS15169 US Google LLC`。
* **Success**: 受信に成功したパケット数。
* **Loss**: 損失したパケット数。
* **Loss Ratio**: パケット損失率。色の境界値は設定可能 (以下はデフォルト)。
  * **緑**: 0%〜20% &nbsp;|&nbsp; **オレンジ**: 20%〜80% &nbsp;|&nbsp; **鮮やかな赤**: >80%
* **RTT / Avg / Jitter**: 往復時間 (Round Trip Time) の最新/平均/ジッタ値。色の境界値は設定可能 (以下はデフォルト)。
  * **RTT**: 緑 (≤50ms) / オレンジ (≤200ms) / 赤 (>200ms)
  * **Jitter**: 緑 (≤10ms) / オレンジ (≤50ms) / 赤 (>50ms)
  * `--rtt-warn/--rtt-crit`、`--jitter-warn/--jitter-crit`、`--loss-warn/--loss-crit`、または YAML の `thresholds:` ブロックで上書きできます。
* **Size**: 送信パケットのペイロードサイズ。
* **MTU**: 送信に使用されているインターフェイスの MTU。
* **TTL**: 最後のパケットの生存時間 (Time To Live)。
* **Error**: 最新エラーの短縮メッセージを表示 (赤色)。詳細は Log ペインに表示されます。
* **Last Loss**: 最後にパケットロスが発生してからの経過時間。

## Paris Traceroute

通常の traceroute は TTL ごとに ICMP の Sequence 番号を変えるため、ECMP（等コスト多経路）ルータがプローブごとに異なる経路へハッシュし、実際には存在しないホップ（ファントムホップ）が現れることがあります。

Paris Traceroute（`--paris` / `-P`）は、全 TTL プローブで ICMP の Identifier と Sequence を固定します。ルータがフローハッシュに使う ICMP チェックサムが一定に保たれるため、全ホップが同一経路を通り、正確で一貫性のある経路が得られます。

このモードが有効なとき、ペインのタイトルが **Paris Traceroute Monitor** に変わります。

## Traceroute Monitor ペイン

* `-T` / `--traceroute` 指定時のみ表示されます。
* 最大 30 ホップまで探索し、Host と Route の 2 カラム形式で結果を表示します。
* 複数ターゲット指定時は行で区切って一覧表示します。
* 起動後に一度 traceroute を実行し、その後 10 分ごとに自動更新します。

## MTR Monitor ペイン

* `-M` / `--mtr` 指定時のみ表示されます。
* 各ターゲットへの経路を発見した後、全ホップに対して毎秒 TTL-limited ICMP プローブを送り続け、ホップ単位のロス率・レイテンシを集計します。
* ホップ発見は起動時に行い、10 分ごとに自動再発見してルート変化に追従します。
* 応答のないホップ (`*`) は 100% ロスとしてカウントされます。
* ヘッダ行には `SrcIP -> DstIP`（ホスト名指定時は `hostname (SrcIP -> DstIP)`）を表示します。
* `-T` (Traceroute ペイン) と同時使用可能で、両ペインが並列表示されます。
* 表示カラム:
  * **Hop** — TTL ホップ番号
  * **Host** — ホップの IP アドレス (`-a` 指定時は AS 番号と国コードも表示。例: `1.2.3.4 (AS15169 US)`)。応答なし時は `*`
  * **Loss%** — このホップのパケットロス率 (緑 / オレンジ / 赤)
  * **Snt** — 送信プローブ数の累計
  * **Recv** — 受信成功数の累計
  * **Last** — 直近プローブの RTT
  * **Avg** — RTT の平均値
  * **Min** — RTT の最小値
  * **Max** — RTT の最大値
  * **Jitter** — パケット遅延変動 (RFC 1889 スムージング)
* 端末幅が狭い場合、**Recv** / **Min** / **Max** / **Jitter** カラムは自動的に省略されます (コンパクトモード)。
* MTR 統計は `-j` JSON エクスポートに `mtr_hops` フィールドとして含まれます。
* **経路変化 (Route Flap) 検知** — 再発見時に経路が変化した場合、対象ターゲットのヘッダに `[FLAP ×N HH:MM:SS]` バッジを表示し、Log ペインに黄色いアラート (例: `[route flap google.com: hop 3: 10.0.0.2 → 10.0.0.9]`) を記録します。

## HTTP Monitor ペイン

* `-H` / `--http` 指定時のみ表示されます。
* Ping 間隔と同じ頻度で HTTP/HTTPS GET リクエストを送信し、ステータスコードとレスポンスタイムを記録します。
* カンマ区切りまたはフラグの繰り返しで複数 URL を指定できます (例: `-H https://a.example.com,https://b.example.com`)。
* YAML ホストファイルの `http:` リストにも指定可能です。
* 表示カラム:
  * **URL** — 監視対象のエンドポイント
  * **Status** — `Up` (緑: 2xx–3xx) / `Down` (赤: 4xx–5xx) / `Error` (赤: 接続エラー) / `Checking...` (グレー: 初期状態)
  * **Code** — HTTP ステータスコード (例: `200`, `503`)、エラー時は `-`
  * **Last** — 直近リクエストのレスポンスタイム
  * **Min** — 最小レスポンスタイム (Up 応答のみ)
  * **Avg** — 平均レスポンスタイム (Up 応答のみ)
  * **Max** — 最大レスポンスタイム (Up 応答のみ)
  * **Up** — Up 判定の累計回数
  * **Down** — Down + Error 判定の累計回数
  * **Since** — 最終ステータス変化からの経過時間
* 端末幅が狭い場合、**Min** / **Avg** / **Max** / **Since** カラムは自動的に省略されます (コンパクトモード)。
* ステータス変化は Log ペインに記録されます (例: `HTTP https://example.com: Up → Down`)。
* HTTP チェック結果は `-j` JSON エクスポートに `http_checks` フィールドとして含まれます。

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
