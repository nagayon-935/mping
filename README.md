# mping

**mping** は Go言語で書かれたターミナルベースのマルチターゲット Ping ツールです。複数のホストに対して同時に Ping を実行し、パケットロス率、RTT、TTL などの統計情報をリアルタイムで見やすい TUI (テキストユーザーインターフェース) で監視できます。

![Go Version1.24](https://img.shields.io/badge/go-v1.24-blue "Go Version1.24")![MIT License](https://img.shields.io/badge/license-MIT-blue "MIT License")[![Coverage Status](https://coveralls.io/repos/github/nagayon-935/mping/badge.svg?branch=main)](https://coveralls.io/github/nagayon-935/mping?branch=main)

## 特徴

* **複数ターゲットへの Ping**: 複数のホストを並行して監視できます。
* **リアルタイム統計**: パケットロス、RTT、TTL、エラーメッセージをリアルタイムで更新します。
* **TUI ダッシュボード**: 黒背景固定の視認性の高いテーブル表示を採用。ウィンドウ幅に応じて列幅を自動再配分し、狭い場合は 1 ターゲット 2 行のコンパクトレイアウトへ自動切替します。
* **色分けによる警告**: パケットロス率/RTT/Jitter に応じて直感的に状況を把握できます。閾値を超えると Log ペインにアラートが記録されます。
* **詳細な設定**: インターフェイス、ソースIP、パケットサイズ、送信回数などの柔軟な指定が可能です。

* **YAML ホストリスト**: ホスト一覧をファイルで管理できます。
* **Traceroute ペイン**: ルートを簡易表示できます。
* **PMTU 探索**: DF 付きのパケットサイズ探索を実行できます。
* **自動ソースIP検出**: 指定がない場合でも、実際に通信に使用されているローカルIPを自動的に表示します。
* **RTT グラフ**: 各グラフは縦軸 0〜100ms、横軸 30 秒の固定レンジで表示されます。
* **CSV ログ出力**: 実行結果を統計情報とともにファイルに保存できます。

## インストール

### ソースコードからビルド

必須要件: Go 1.24 以上

```bash
git clone https://github.com/yourusername/mping.git
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
| `--privileged` | `-p` | 特権 ICMP ソケットを使用するか (true/false) | `true` |
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

### キー操作

| キー | 動作 |
| :--- | :--- |
| **q** | アプリケーションを終了する |
| **s** | Ping 送信を一時停止する |
| **S** | Ping 送信を再開する (**s** で一時停止した後のみ有効) |
| **R** | 全ての統計情報とログをリセットする |
| **Tab** | フォーカスを切り替える (Table / Traceroute / RTT Graphs / Log) |
| **↑/↓/PgUp/PgDn** | Table/Traceroute/RTT Graphs をスクロール (各ペインにフォーカス時) |

## 表示項目 (TUI カラム)

* **Hostname**: 指定されたホスト名。
* **Src IP**: 送信に使用されているローカル IP アドレス。
* **Dst IP**: 名前解決された宛先 IP アドレス。
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

## Traceroute ペイン

* `--traceroute` 指定時のみ表示されます。
* 最大 30 ホップまで探索し、結果をスクロール可能な表形式で表示します。

## PMTU 探索

* `--discovery-mtu` 指定時に最大 payload サイズを探索します。
* 探索は DF 付き ICMP を使い、payload 上限は 9872 から開始します。
* 探索結果は `Size` に反映されます。

## ライセンス

MIT
