# mping

**mping** は Go言語で書かれたターミナルベースのマルチターゲット Ping ツールです。複数のホストに対して同時に Ping を実行し、パケットロス率、RTT、TTL などの統計情報をリアルタイムで見やすい TUI (テキストユーザーインターフェース) で監視できます。

![Go Version1.24](https://img.shields.io/badge/go-v1.24-blue "Go Version1.24")![MIT License](https://img.shields.io/badge/license-MIT-blue "MIT License")[![Coverage Status](https://coveralls.io/repos/github/nagayon-935/mping/badge.svg?branch=main)](https://coveralls.io/github/nagayon-935/mping?branch=main)![Go Report Card](https://goreportcard.com/badge/github.com/nagayon-935/mping)

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

## インストール

### ソースコードからビルド

必須要件: Go 1.24 以上

```bash
git clone https://github.com/nagayon-935/mping.git
cd mping
go build -o mping ./cmd/main
```

## 使い方

`mping` はデフォルトで最も正確な結果 (TTLを含む) を得るために Raw ICMP ソケットを使用するため、通常は `sudo` または `CAP_NET_RAW` ケーパビリティが必要です。

```bash
# 基本的な使い方 (要 sudo)
sudo ./mping google.com 1.1.1.1 8.8.8.8

# インターフェイスを指定して実行
sudo ./mping -I en0 google.com

# パケットサイズ (100バイト) と 送信回数 (10回) を指定
sudo ./mping -s 100 -c 10 google.com

# ログを CSV ファイルに出力
sudo ./mping -o results.csv google.com

# YAML からホストを読み込み
sudo ./mping -f hosts.yaml

# IPv4 のみを強制
sudo ./mping -4 google.com

# IPv6 のみを強制
sudo ./mping -6 google.com

# Traceroute ペインを表示
sudo ./mping -T google.com

# PMTU 探索 (payload 上限 9872 から探索)
sudo ./mping -m google.com

# ポート疎通確認 (443/tcp)
sudo ./mping -p 443/tcp google.com

# 複数ポートをカンマ区切りで指定
sudo ./mping -p 443/tcp,53/udp google.com 8.8.8.8

# Traceroute と Port Monitor を同時に表示
sudo ./mping -T -p 443/tcp google.com
```

### hosts.yaml の例

```yaml
- google.com
- 1.1.1.1
```

```yaml
hosts:
  - google.com
  - 1.1.1.1
```

### オプション

| フラグ | 短縮形 | 説明 | デフォルト |
| :--- | :--- | :--- | :--- |
| `--interval` | `-i` | Ping の送信間隔 (ミリ秒) | `1000` |
| `--timeout` | `-t` | Ping のタイムアウト (ミリ秒) | `1000` |
| `--file` | `-f` | ホスト一覧の YAML ファイルパス | `""` |
| `--traceroute` | `-T` | Traceroute ペインを表示する | `false` |
| `--discovery-mtu` | `-m` | 最大 payload サイズを DF で探索する | `false` |
| `--interface` | `-I` | 使用するネットワークインターフェイス名 (例: `eth0`) | `""` |
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
| **Tab** | フォーカスを切り替える (Ping Monitor / Traceroute Monitor / Port Monitor / RTT Graphs / Log) |
| **↑/↓/PgUp/PgDn** | Table/Traceroute/RTT Graphs をスクロール (各ペインにフォーカス時) |

## 表示項目 (TUI カラム)

* **Src IP**: 送信に使用されているローカル IP アドレス。
* **Dst IP**: 名前解決された宛先 IP アドレス。ドメイン名で指定した場合は `domain (IP)` の形式で表示。
* **Success**: 受信に成功したパケット数。
* **Loss**: 損失したパケット数。
* **Loss Ratio**: パケット損失率。
  * **緑**: 0% 〜 20%
  * **オレンジ**: 20% 〜 80%
  * **鮮やかな赤**: 80% 超
* **RTT / Avg / Jitter**: 往復時間 (Round Trip Time) の最新/平均/ジッタ値。
  * **RTT**: 緑 (<=50ms) / オレンジ (<=200ms) / 赤 (>200ms)
  * **Jitter**: 緑 (<=10ms) / オレンジ (<=50ms) / 赤 (>50ms)
* **Size**: 送信パケットのペイロードサイズ。
* **MTU**: 送信に使用されているインターフェイスの MTU (最大転送単位)。
* **TTL**: 最後のパケットの生存時間 (Time To Live)。
* **Error**: 最新エラーの短縮メッセージを表示 (赤色)。詳細は Log ペインに表示されます。
* **Last Loss**: 最後にパケットロスが発生してからの経過時間。

## Traceroute Monitor ペイン

* `-T` / `--traceroute` 指定時のみ表示されます。
* 最大 30 ホップまで探索し、Host と Route の 2 カラム形式で結果を表示します。
* 複数ターゲット指定時は各ターゲットを行で区切って一覧表示します。
* 起動後に一度 traceroute を実行し、その後 10 分ごとに自動更新します。

## Port Monitor ペイン

* `-p` / `--port` 指定時のみ表示されます。
* 指定したポートへの TCP/UDP 疎通確認をリアルタイムで実行します。
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

* `--discovery-mtu` 指定時に最大 payload サイズを探索します。
* 探索は DF 付き ICMP を使い、payload 上限は 9872 から開始します。
* 探索結果は `Size` に反映されます。

## ライセンス

MIT
