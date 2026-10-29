<p align="center">
  <img src="./assets/logo.jpg" alt="mping logo" width="200">
</p>

# mping

**mping** は Go言語で書かれたターミナルベースのマルチターゲット Ping ツールです。複数のホストに対して同時に Ping を実行し、パケットロス率、RTT、TTL などの統計情報をリアルタイムで見やすい TUI (テキストユーザーインターフェース) で監視できます。

![Go Version1.26](https://img.shields.io/badge/go-v1.26-blue "Go Version1.26")![MIT License](https://img.shields.io/badge/license-MIT-blue "MIT License")[![Coverage Status](https://coveralls.io/repos/github/nagayon-935/mping/badge.svg?branch=main)](https://coveralls.io/github/nagayon-935/mping?branch=main)

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
| Windows | amd64 | **実験的** — 下記参照 |

> **Windows について** — リリースには Windows 版も含まれますが、CI で確認しているのはビルドが通ることだけで、Windows 上での動作テストは行っていません。Raw ICMP ソケットを使うため、管理者として起動したシェルから実行してください。Windows では TTL は表示されず、`-I`（インターフェイス指定）は効果がありません。`-m`（PMTU 探索）は動作未確認です。

> **権限について** — mping は正確な TTL を取得するために Raw ICMP ソケットを使用します。Linux では `setcap cap_net_raw+ep` での権限付与を推奨します (`install.sh` が自動で処理します)。macOS では `setuid` を使用します。`setuid` でインストールした場合も、root 権限を使うのは raw ソケットを開くときだけです。読み書きするファイル（`-f`・`include:`・`-o`・`-j`・保存するレポート）はすべて起動したユーザーの権限でアクセスするため、そのユーザーが本来アクセスできないファイルの読み取りや書き換えには使えません。

> **ターミナルの互換性** — Linux や macOS の標準ターミナルでは、色が正しく描画されないことがあります。色が正しく表示されない場合は、モダンなターミナルエミュレータ（iTerm2, Alacritty, kitty など）の使用を検討してください。

## インストール

### リリース済みバイナリ (推奨)

[Releases](https://github.com/nagayon-935/mping/releases) ページから対応プラットフォームのアーカイブをダウンロードして展開し、同梱の `install.sh` を実行します。

#### Linux (amd64)

```bash
# ダウンロードと展開 (v0.4.5 は例です。最新バージョンは Releases ページで確認してください)
curl -LO https://github.com/nagayon-935/mping/releases/download/v0.4.5/mping-v0.4.5-linux-amd64.tar.gz
tar -xzf mping-v0.4.5-linux-amd64.tar.gz

# インストール (setcap で CAP_NET_RAW を付与。setcap が無い場合は setuid にフォールバック)
sudo ./install.sh
```

#### Linux (arm64 — Raspberry Pi, AWS Graviton など)

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

### ソースコードからビルド

**必須要件:** Go 1.26.10 以上

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
dns-server: 8.8.8.8
resolve-all: true
web: true
web-port: 8080
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

実行可能なサンプルは [examples/hosts-groups.yaml](examples/hosts-groups.yaml) を参照（`mping -f examples/hosts-groups.yaml`）。

### 大量のホストを追加する: パターン・表示名・include

ホストの指定（`hosts:`・`groups[].hosts:`・include ファイル）には、複数ホストに展開されるパターンを書けます。

| パターン | 展開結果 |
|---|---|
| `10.0.0.1-20` | `10.0.0.1` 〜 `10.0.0.20`（最終オクテットの範囲） |
| `10.0.0.250-10.0.1.5` | 範囲内のすべての IPv4 アドレス |
| `192.0.2.0/28` | `192.0.2.1` 〜 `192.0.2.14`（IPv4 の /30 以上はネットワーク・ブロードキャストアドレスを除外。IPv6 の CIDR は全アドレス） |
| `core-sw{01..12}.lab` | `core-sw01.lab` 〜 `core-sw12.lab`（ゼロ埋めを維持。`{N..M}` を複数書くと全組み合わせ） |

1パターンの展開は最大 1024 ホスト、hosts ファイル全体（include を含む）は最大 4096 ターゲットです。IPv6 の範囲指定は非対応のため、CIDR で指定してください。

`name` を付けると、アドレスの代わりにその名前を表示します（IP は名前の横に表示されます）。名前は重複不可で、他のエントリのホストと同じ文字列にもできません。複数ホストに展開されるパターンには名前を付けられません。CSV / JSON 出力とレポートでも、ホストの代わりに名前が記録されます（解決した IP は引き続き記録されます）。

```yaml
hosts:
  - {host: 10.0.0.1, name: core-sw01}
```

`include:`（トップレベルまたはグループごと）で、テキスト / CSV ファイルからホストを追加できます（`.csv` ファイルや NetBox CSV は `mping -f hosts.csv` のように `-f` で直接指定することも可能です）。パスは YAML ファイルからの相対パスです。各行は `host[,name[,dscp]]` 形式で、`#` 以降はコメントです。先頭行の1列目が `host` または `hostname` の場合はヘッダとして読み飛ばします。ホストは IP アドレスかホスト名（英数字・`.`・`-`・`_`）である必要があり、ファイルは mping を実行するユーザーが読める必要があります。include ファイルも YAML と同様に変更が監視され、自動で反映されます（まだ存在しないファイルを YAML に追加した場合も、作成した時点で反映されます）。

NetBox の IP Addresses CSV エクスポート（`IP Address` 列を含む CSV）も、`-f` / `--file` での直接指定および `include:` の両方でそのまま読み込めます。プレフィックス長（`/23` など）は自動的に除去され、`Status` が `Active`（または空）の行が対象になります。表示名には `DNS name`（空なら `Parent`）が使用され、同名になる場合は `Interface` や IP アドレスを付与して自動的に一意化されます。

```yaml
groups:
  - name: Core
    hosts: ["core-sw{01..04}.lab"]
  - name: Branches
    include: [branches.csv, more-branches.txt]
```

サンプルは [examples/hosts-patterns.yaml](examples/hosts-patterns.yaml) と [examples/branches.csv](examples/branches.csv) を参照。


### オプション

| フラグ | 短縮形 | 説明 | デフォルト |
| :--- | :--- | :--- | :--- |
| `--interval` | `-i` | Ping の送信間隔 (ミリ秒) | `1000` |
| `--timeout` | `-t` | Ping のタイムアウト (ミリ秒) | `1000` |
| `--file` | `-f` | ホスト一覧の YAML / CSV ファイルパス | `""` |
| `--traceroute` | `-T` | Traceroute ペインを表示する | `false` |
| `--mtr` | `-M` | MTR Monitor ペインを表示する (経路別ロス/レイテンシの継続計測) | `false` |
| `--dns-server` | `-d` | 名前解決に使用するカスタム DNS サーバー IP アドレス | `""` (OSデフォルト) |
| `--resolve-all` | | 宛先のホスト名を解決して得られたすべての IP を個別のターゲットとして並行監視する | `false` |
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
| `--web` | | Web UI を `http://127.0.0.1:<web-port>/` で提供する (このマシンからのみ接続可。[Web UI](#web-ui-実験的機能) 参照) | `false` |
| `--web-port` | | `--web` の待ち受けポート (1–65535) | `8080` |
| `--no-tui` | | TUI を使わずに実行する ([ヘッドレスモード](#ヘッドレスモード---no-tui) 参照) | `false` |
| `--asn` | `-a` | ターゲット IP の AS 番号を検索して表示する | `false` |
| `--http` | `-H` | ヘルスチェックする URL (例: `https://example.com/health`)。カンマ区切りまたは繰り返し指定で複数可 | `""` |
| `--rtt-warn` | | RTT の warn 閾値 (ミリ秒・オレンジ) | `50` |
| `--rtt-crit` | | RTT の crit 閾値 (ミリ秒・赤) | `200` |
| `--jitter-warn` | | Jitter の warn 閾値 (ミリ秒・オレンジ) | `10` |
| `--jitter-crit` | | Jitter の crit 閾値 (ミリ秒・赤) | `50` |
| `--loss-warn` | | ロス率の warn 閾値 (パーセント・オレンジ) | `20` |
| `--loss-crit` | | ロス率の crit 閾値 (パーセント・赤) | `80` |

> **閾値 (Thresholds)** — `warn` がオレンジ、`crit` が赤の境界値で、Loss Ratio / RTT / Jitter カラムの色分け (および Log ペインのアラート記録) に使われます。各メトリクスで `warn` は `crit` より小さい必要があります。YAML の `thresholds:` ブロックでも設定できます。

### 現在の状態判定と設定チェック

現在の状態と累積統計は分けて扱います。既定では **3回連続失敗で応答不能**、**2回連続応答で応答不能を解除**します。復旧後も直近のロス率・最新RTT・ジッターが閾値を超えていれば警告や異常を表示します。過去の平均・ピークRTTは現在の状態に影響しません。起動直後の1回のタイムアウトで異常にしないよう、直近ロス率の判定は `health-failures` 回以上の計測完了後に行います。一覧のLoss %は従来どおり、完了した計測に対する累積値です。

| オプション (YAMLでも同名のトップレベルキー) | 既定値 | 内容 |
|---|---|---|
| `--health-failures` | `3` | 応答不能と判定する連続失敗回数 (1–10000) |
| `--health-recoveries` | `2` | 応答不能を解除する連続応答回数 (1–10000) |
| `--health-window` | `1m` | 直近ロス率の集計期間 (1秒–1時間、1秒単位で集計)。期間を変更すると直近の集計窓を新しく開始 |
| `--event-output` | 空 | 共通イベントをJSONLファイルに追記 |
| `--http-status` | `200-399` | 期待するHTTPコード・範囲。例 `200-299,401` |
| `--http-follow-redirects` | `true` | 最大10回までリダイレクトを追う。falseなら最初の応答を判定 |
| `--http-header` | 空 | 繰り返し指定できる `Name=value` ヘッダー。YAMLではリスト |

JSONの `health` には状態・理由・応答不能判定・連続回数・直近ロス率・最終計測時刻・最終応答時刻を含みます。直近の集計値は `evaluated_at` (最後の計測完了または判定ルールの変更時点) の窓を表し、一時停止中も判定理由と集計値をそろえて保持します。`measurement_state` (`running` / `finished` / `stopped`) はICMP計測の状態で、Webサーバーの `state` とは別です。TUIでは送信回数の上限到達後も補助監視を継続できます。グラフは実際の計測時刻を使い、一時停止中は空白として表示します。

JSONの各ターゲットには `levels` (`loss` / `last_rtt` / `jitter`) も含まれ、設定された閾値に対する判定を `none` / `ok` / `warn` / `crit` で表します。MTR の各ホップには `loss_level`、ポート・HTTP チェックには `level` (最初の結果が出るまでは `pending`) が付きます。TUI と Web UI はこの値で色分けします。

```bash
mping --check-config -f examples/monitoring.yaml --interval 500
```

`--check-config` (CLIのみ) は有効な設定、展開後の対象・対象数・グループとCLIで上書きした項目を出力します。計測・待ち受け・UI・出力ファイル作成は行わず、raw socketの権限も不要です。`--resolve-all` 指定時はDNS検索を行うことがあります。ヘッダーは名前だけ表示し、値は表示しません。CLI指定はYAMLより優先されます。

ヘッダー値には `Authorization=Bearer ${HEALTH_TOKEN}` のような環境変数参照を使えます。変数未定義や不正なヘッダーは値を表示せずエラーにします。同一実行の全URLに共通設定を使うため、認証情報を分けたい場合は別々に起動してください。別オリジンへのリダイレクト (HTTPS→HTTPを含む) には設定ヘッダーを送信しません。URLは認証情報・フラグメントを含まないHTTP(S)のみ対応します。設定チェックとレポートにはヘッダー値を含めません。

状態変化はTUI・Web・ヘッドレスで共通の判定結果を使います。同一エラーの連続失敗は `count` と `last_at` に集約し、JSONLには発生時点の更新を追記します。これは計測ごとのCSV・定期的なJSONスナップショットとは別の出力です。書き込みキューが満杯の場合や保存失敗はstderrに通知し、通常終了時にキューを書き切ります。Web設定・CSV/イベント出力先・セッションの終了期限は起動時の設定を維持し、変更時は再起動の案内を出します。ポート・ネットワーク設定の変更は測定を再構築して反映します。[設定例](examples/monitoring.yaml)も参照してください。

### キー操作

| キー | 動作 |
| :--- | :--- |
| **q** | アプリケーションを終了する |
| **s** | 全ての測定を停止し、最後の統計情報を保持する |
| **S** | 統計情報を保持して、新しいワーカーで測定を再開する (**s** の後に有効) |
| **R** | 統計情報とUIログをリセットする。停止中は測定を再開しない |
| **a** | 「ホスト追加」ダイアログを開く — ホスト名または IP を入力して Enter で実行時に追加 |
| **d** | 一覧で選択したホスト、または詳細表示中のホストの削除確認を開く |
| **Tab** | フォーカスを切り替える: Ping Monitor → Traceroute Monitor → MTR Monitor → Port Monitor → HTTP Monitor → RTT Graphs → Log |
| **↑ / ↓ / PgUp / PgDn** | Ping 一覧ではホストを選択。他のペインや詳細ではスクロール |
| **Enter** | 選択ホストの詳細を開く（統計・経路・MTR・ポート・RTT グラフ・対象別イベント） |
| **Esc** | 詳細から一覧へ戻る。最大化中は元の配置へ戻る |
| **f** | フォーカス中のペインを折りたたみ／展開する |
| **z** | フォーカス中のペインを最大化／元の配置へ戻す |
| **w** | 一覧では全体、詳細では選択対象の調査結果を保存する |
| **b** | Web UIのtoken付きURLを表示・コピー (`--web` 指定時) |

> ホストの追加・削除では、既存ターゲットの統計・グラフ・経路・ポート結果と HTTP 監視を保持します。停止中の編集では測定を再開しません。削除後の再追加は新しい測定です。`--count` の既存対象の残り回数と `--duration` の終了期限は維持します。削除・停止で中断した送信済みプローブは JSON の `cancelled` に記録し、パケットロスには加算しません。YAML のグループ・表示名・判定閾値・対象の追加削除でも既存対象を保持します。計測方法やネットワーク設定の変更では、変更理由をログに出して測定を再構築します。
>
> ホストの選択・削除は対象 ID で行うため、同一表示名や DSCP 違いの対象も個別に扱えます。セッション内の安全な ICMP ID を使い切った場合（初期起動・追加・再開の合計で最大32,768対象 worker）、再起動が必要です。

削除確認にはホスト名・対象 ID・IP・DSCP を表示します。最初は「キャンセル」が選択され、Enter だけでは削除されません。Tab または左右キーで「削除」を選び、Enter で確定します。Esc でキャンセルでき、`d` の連打では確定しません。確認中も測定を継続し、キャンセル時は元のフォーカスとスクロール位置へ戻ります。最後のホストでは「終了するには q キーを押してください」と案内します。その画面で `q` を押すと通常の終了処理を行い、Enter / Esc なら測定画面へ戻ります。

折りたたみ・最大化は一覧画面で利用できます。折りたたんだペインも Tab で選択でき、`f` で再展開できます。非表示中も測定は継続し、展開後は最新結果を表示します。最大化中の Tab はそのペインに留まります。

停止・再開・リセットはキー入力の順に処理します。実行中のリセットでは、リセット前に送ったプローブの結果を統計に加えず、有効な経路・ポート・HTTP監視を作り直します。`--count` の残り回数は維持し、再開時には新しい送信回数枠を開始します。停止中のリセットではPingとMTRのカウンターをクリアし、最後の経路とポート・HTTP結果は保持します。終了時は測定ワーカーの完了を待ってから最終JSONを書き出します。

### 調査結果の保存

`w` で保存画面を開き、ファイル名を入力して Enter または「Save」で保存します。末尾が `.txt` ならテキスト、`.json` なら JSON と判定し、入力欄の下に形式を表示します。拡張子がなければ `.txt` を補います。それ以外の拡張子では保存せず、修正を案内します。Tab で入力欄とボタンを切り替え、Esc で戻れます。

一覧からは全体と保持している削除対象・HTTP 結果、詳細からは選択対象だけを保存します。保存中も測定は継続し、既存ファイルは上書きしません。

レポートの権限は所有者だけが読み書きできる `0600` です。sudo 起動でも、保存完了前に sudo を起動したユーザーの所有者・グループへ変更します。setuid 起動では起動したユーザーとしてレポートを作成します。所有者の設定に失敗した場合、保存先の完成ファイルは作りません。

## シェル補完

mping は `mping --help` と同じフラグ定義から bash/zsh/fish 用の補完スクリプトを生成します。フラグ名、`-f`/`-o`/`-j` のファイルパス、`-I` のネットワークインターフェース名、`mping completion bash|zsh|fish` を補完できます。ファイルパスとインターフェース名は `--file=...` / `--interface=...` 形式や `-Ien` のような短縮フラグに続けて入力する形式にも対応します。スクリプト生成とインターフェース一覧取得に `sudo` は不要です。

対応シェルは `mping completion --help` でも確認できます。ソースコードから試す場合は、先に `make build` を実行し、以下の `mping` を `./mping` に置き換えてください。

### Bash

```bash
# 現在のシェルで有効化。次回以降も使う場合は ~/.bashrc に追記
source <(mping completion bash)
```

### Zsh

現在のシェルで有効化します（補完が初期化されていない場合は先に `compinit` を実行）:

```zsh
autoload -Uz compinit
compinit
source <(mping completion zsh)
```

次回以降も使う場合は、自分が書き込めるディレクトリにスクリプトを保存します:

```zsh
mkdir -p ~/.zsh/completions
mping completion zsh > ~/.zsh/completions/_mping
```

以下を `~/.zshrc` に追記し、新しいシェルを起動してください。既に `compinit` を実行している場合（シェルフレームワークによる実行を含む）、`fpath` の設定をその前に置きます:

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

保存したスクリプトは mping の更新後に再生成すると、新しいフラグも補完できます。`completion` というホスト名を監視する場合は `mping -- completion` と指定してください。

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


## Traceroute Monitor ペイン

* `-T` / `--traceroute` 指定時のみ表示されます。
* 最大 30 ホップまで探索し、Host と Route の 2 カラム形式で結果を表示します。
* 複数ターゲット指定時は行で区切って一覧表示します。
* 起動後に一度 traceroute を実行し、その後 10 分ごとに自動更新します。

## MTR Monitor ペイン

* `-M` / `--mtr` 指定時のみ表示されます。
* 各ターゲットへの経路を発見した後、全ホップに対して毎秒 TTL-limited ICMP プローブを送り続け、ホップ単位のロス率・レイテンシを集計します。
* ホップ発見は起動時に行い、10 分ごとに自動再発見してルート変化に追従します。初回に応答しなかったホップも毎回計測し、応答が得られた時点で表示します。Tracerouteも監視中のIPが取得できている場合はそのIPを利用し、別のDNS応答先との比較を避けます。
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

## Web UI (実験的機能)

`--web` を付けると TUI と並行して Web サーバーが起動し、mping を実行しているマシンのブラウザから同じ統計を確認できます。

```bash
mping --web google.com 1.1.1.1      # http://127.0.0.1:8080/ を開く
```

- 待ち受けは loopback のみ (`127.0.0.1`、利用可能なら `[::1]` も) で、他のマシンからは接続できません。`Host` / `Origin` ヘッダがこのサーバー以外のリクエストは拒否します (DNS rebinding・クロスサイト対策)。
- `--web` / `--web-port` は起動時にのみ読み込まれます。hosts ファイルのリロードではサーバーは再起動しません (hosts ファイルで変更すると「再起動が必要」という警告を Log ペインに表示します)。
- PCのダッシュボードはブラウザの横幅・高さ全体を使います。上段にPing monitorと選択ホストの **Summary**、下段に経路監視と **RTT graphs** を配置し、各ペイン内でスクロールします。狭い画面では縦に並べます。Summaryはタブを設けず、統計・有効なポート監視・対象操作をまとめます。
- 経路表示は **Tracerouteのみ有効 → Tracerouteペイン**、**MTRのみ有効 → MTRペイン**、**両方有効 → MTRペインのみ** です。どちらもホスト選択なしで全対象を表示し、一覧のフィルターに連動します。Tracerouteは番号付きの縦経路にAS／PTRをオプションに応じて表示します。MTRは対象ホストと列の見出しを固定し、ペイン内を縦横にスクロールできます。MTRはTTL／Hop Limitを制限したICMP Echo方式で、UDPモードはありません。
- **Log** は上部ナビゲーションから開く独立画面で、全ホストの最新200件のイベントを表示します。選択ホストや一覧のフィルターには連動しません。有効な **HTTP** 監視も独立画面にまとめます。Monitorに戻ると選択中のSummaryと各ペインのスクロール位置を復元します。
- RTTグラフは共通の時間軸と、読みやすい等間隔4区間の共通縦軸を初期設定にし、ms単位を表示します。Per targetで個別スケールにも切り替えられます。どれかのグラフにカーソルを合わせると全対象の同じ時刻を比較できます。
- 一覧は画面の幅に収まる限りの列を表示し、狭いときは重要度の低い列から隠します (状態・ホスト・ロス率・最新RTTは常に表示)。状態の理由は各行に表示し、判定には連続失敗・連続応答・直近ロス率・最新 RTT・ジッターを使います。平均・最小・ピークRTTはSummaryにまとめます。履歴はRTT graphsに集約し、グラフの数値はカーソル位置の値だけ表示します。広い画面では一覧と詳細を並べて表示します。閲覧専用の場合は **Enable controls** からtokenまたはTUIでコピーしたURL全体を貼り付け、再読み込みせずに操作を有効にできます。入力したtokenは検証成功後に保存し、接続失敗とtokenの拒否は別のメッセージで案内します。
- 列幅・行高は計測値の更新で変わらないレイアウトです。表示幅に応じて列数・文字サイズを調整し、Status・Host・Loss %・最新RTTを残します。倍率の選択は不要です。Inspectの横スクロール位置も更新時に保持します。長い名前や IPv6 は省略表示し、全文はツールチップや詳細で確認できます。大きなカウントは M/B/T、非常に大きな RTT は指数表記にし、元の値はツールチップに残します。[列幅と表示桁数の仕様](internal/web/COLUMNS.md)を参照してください。
- **ブラウザからの操作**: `--web` 指定時はTUIヘッダーに接続先と **b** キーの案内を常設します。一覧・ホスト詳細で **b** を押すと、現在のtoken付きURLをいつでも表示できます。**Copy URL**（Enter）で端末にクリップボードへのコピーを依頼し、**Esc** で元の画面に戻ります。端末がコピーに対応していない場合は表示されたURLを選択してください。Logの保持件数やスクロール位置には依存しません。起動時には従来どおりLogにも表示します。表示されるリンク (例: `Web UI: http://127.0.0.1:8080/#token=…`) を開くと、ホストの追加・確認ダイアログを経たターゲットの削除・統計のリセットができます。token は起動ごとに生成され、操作内容は TUI の Log ペインにも記録されます。token なし (または以前の起動時の token) で開いた場合は閲覧のみです。同じ token でTUIと共通の Stop / Restart、レポート保存も利用できます。**Report** ボタンを押すと、**JSON／Text** と保存するファイル名を指定できます。拡張子は選択した形式に合わせ、CancelやEscでダウンロードせずに閉じられます。
- 状態別の件数ボタンから一覧・経路・グラフを絞り込めます。検索と状態を組み合わせ、表示件数／全件数を確認できます。列見出しや選択欄からグループをまたいで並べ替えでき、ホストの下にはグループ名を残します。計測値が変わっても順序は固定し、**Refresh order** で順位を更新します。**Configured order** で元のグループ順に戻り、固定中に追加したホストは末尾に表示します。
- 選択ホストは一覧・経路・グラフで強調します。**Selected host** を押すと各ペイン内の対象へ移動します。選択や計測更新で他のペインを自動スクロールしません。選択対象が絞り込みから外れてもSummaryを維持し、その旨を表示します。Summaryは現在・直近の状態、開始／リセット以降の統計、ネットワーク詳細の順です。
- ブラウザの接続・測定の稼働状態・ホストの健全性を分け、**Last probe** に実際の最終プローブ時刻を表示します。再接続中は以前の値であることを案内します。通知はレイアウトを動かさずに重ね、エラーは閉じるまで残します。**Stop all / Restart all** は統計を維持します。削除・リセットは対象範囲を明示し、Cancelに初期フォーカスを置いた期限のない確認ダイアログで実行します。狭い画面では操作を **Actions** にまとめます。
- **?** でキー操作の説明と文字ショートカットの無効化設定を開きます。**/** で検索、一覧内の **j/k** または **↓/↑** でフォーカス移動、**Home/End** で先頭／末尾、**Enter** で選択、**Delete** で削除確認、**w** でReport、**Esc** で検索を離れる／ダイアログを閉じる操作ができます。入力欄・IME変換・ブラウザの修飾キー操作は妨げません。グラフにフォーカスして **←/→**・**Home/End** を使うと、時刻とRTT／欠測／無応答をテキストでも確認でき、**Esc** でカーソルを消せます。
- 時刻はシステムのタイムゾーン・24時間表記です。AS・PTR・PMTUの表示は、SummaryやMTR内も含めて各オプションに連動します。
- 同じデータを JSON でも取得できます:

| エンドポイント | 内容 |
|---|---|
| `GET /api/v1/snapshot` | 現在の統計 (`snapshot` 配下は `--json-output` と同じスキーマ)、`meta` (有効な機能・閾値・グループ)、`state` (`starting` / `running` / `reloading` / `stopped`) |
| `GET /api/v1/events?limit=200` | 全ターゲットのイベントを新しい順に 1 つのリストで返す (ホスト名付き、`limit` は 1–1000) |
| `GET /api/v1/history?n=60` | 全ターゲットの直近 RTT を 1 回のレスポンスで返す (`n` は 1–3000) |
| `GET /api/v1/stream` | Server-Sent Events。接続時と、統計が変化している間は最大 1 秒に 1 回 `snapshot` イベントを送信 |
| `GET /api/v1/targets/{id}/history?n=300` | 互換用 `rtt_ms`、時刻付き `samples` (`at`, `rtt_ms`)、取得時刻 `captured_at`。古い順で `null` は応答なし (`n` は 1–3000) |
| `GET /api/v1/targets/{id}/events` | ターゲットの記録済みイベント (DNS 変化・経路フラップ・ロス) |
| `GET /api/v1/session` | 有効な `X-Mping-Token` ヘッダ付きなら `{"control": true}` |
| `POST /api/v1/targets` | ホスト追加。JSON `{"host": "example.com"}`、`X-Mping-Token` が必要 |
| `DELETE /api/v1/targets/{id}` | ターゲット削除。`X-Mping-Token` が必要 |
| `POST /api/v1/reset` | 統計リセット。`X-Mping-Token` が必要 |
| `POST /api/v1/stop`, `POST /api/v1/restart` | 統計を保持して一時停止・再開。`X-Mping-Token` が必要 |
| `GET /api/v1/report?format=json` (`text` も可) | サーバーにファイルを作らずレポートをダウンロード。`X-Mping-Token` が必要 |

## ヘッドレスモード (`--no-tui`)

`--no-tui` を付けると TUI を使わずに実行します。小型機でのサービス常駐などを想定しており、通常は `--web`・`--json-output`・`--output` と組み合わせます。

```bash
mping --no-tui --web -f hosts.yaml
```

- TUI の Log ペインに出るログ (経路フラップ、リロード通知、Web からの操作など) をプレーンテキストで stdout に出力し、終了時には通常どおり統計サマリーを表示します。状態変化のアラートも共通の判定処理から出力します。
- Ctrl-C / SIGTERM、`--count` の完了、`--duration` の経過で終了します。hosts ファイルのリロードでは終了しません (保留中のリロードが Ctrl-C / SIGTERM より優先されることはありません)。終了処理が止まった場合は、もう一度 Ctrl-C を押すと即座に終了します。
- Web UI の操作用リンクには秘密の token が含まれるため、stdout が端末のときだけ表示します。無人運用では環境変数 `MPING_WEB_TOKEN` で token を指定し (英数字と `-._~` で 16 文字以上。コマンドラインやシェル履歴に残さず、systemd の `EnvironmentFile=` などで渡してください)、`http://127.0.0.1:8080/#token=<指定した token>` を開いてください。`MPING_WEB_TOKEN` は TUI 使用時にも有効です。

## ライセンス

MIT
