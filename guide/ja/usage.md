# 使い方とオプション

← [README](../../README.ja.md)

`-4` / `-6` を省略すると、ホスト名は OS と Go のダイヤラーによるアドレス順序・経路・送信元選択に従います。宛先ごとに OS が送信できる IPv4 / IPv6 を選び、IPv4 の固定優先や、無関係な外部ホストを使う IPv6 全体判定は行いません。UDP ソケットを接続してローカルの経路を確認し、データグラムは送りません。Ping の応答確認ではないため、その後の Ping ロスで別のバージョンに切り替えません。定期的な DNS 再解決では選択を見直します。

IP アドレスの直接指定では、そのバージョンと IPv6 の zone を維持します。`-S` は送信元と同じファミリーに制限し、`-I` 単独ではそのインターフェイス上の両ファミリーを利用できます。Windows/FreeBSD では物理インターフェイスへの固定の代わりに、ファミリーごとの送信元アドレスへ固定します。`-4` / `-6` は明示的に固定したい場合に使えます。`--resolve-all` は、現在経路のないものも含め、指定ファミリーの全 DNS アドレスを意図的に監視します。

このファミリー選択は Ping の対象と、その経路・ポート監視に適用します。HTTP の URL は独立して確認するため、`-4` / `-6` だけでは HTTP のアドレスファミリーを固定しません。送信元・インターフェイス指定（`-S` / `-I`）は HTTP 接続にも適用します。

`-c` は指定回数で ICMP 計測を終えますが、TUI は開いたままです。一定時間でアプリケーションを終了するには `--duration 30s`、手動で終了するには **q** を使います。

```bash
# 基本的な使い方 (install.sh 実行後は sudo 不要)
mping google.com 1.1.1.1 8.8.8.8

# インターフェイスを指定して実行
mping -I eth0 google.com

# パケットサイズ (100バイト) と送信回数 (10回) を指定
mping -s 100 -c 10 google.com

# 30秒後にアプリケーションを終了する
mping --duration 30s google.com

# YAML からホストを読み込み
mping -f hosts.yaml

# IPv4 のみを強制
mping -4 google.com

# IPv6 のみを強制
mping -6 google.com

# Traceroute ペインを表示
mping -T google.com

# PMTU 探索 (自動選択したアドレスのバージョンで実行)
mping -m google.com

# 宛先 IP に応じて切り替えるため、IPv4・IPv6 混在でも共通の -m だけ
mping -m 1.1.1.1 2606:4700:4700::1111

# ポート疎通確認 (443/tcp)
mping -p 443/tcp google.com

# 複数ポートをカンマ区切りで指定
mping -p 443/tcp,53/udp google.com 8.8.8.8

# MTR 風の経路別ロス/レイテンシ監視
mping -M google.com

# Traceroute と Port Monitor を同時に表示
mping -T -p 443/tcp google.com

# 色分けの閾値をカスタマイズする (warn = オレンジ, crit = 赤)
mping --rtt-warn 30 --rtt-crit 100 --loss-warn 10 --loss-crit 50 google.com

# ターゲット IP の AS 番号を表示する
mping -a google.com 1.1.1.1
```

> インストールせずに実行する場合 (`setcap`/`setuid` なし) は `sudo` を付けてください:
> ```bash
> sudo ./mping google.com
> ```

## オプション

| フラグ | 短縮形 | 説明 | デフォルト |
| :--- | :--- | :--- | :--- |
| `--interval` | `-i` | Ping の送信間隔 (100–60000 ミリ秒) | `1000` |
| `--timeout` | `-t` | Ping のタイムアウト (10–30000 ミリ秒) | `1000` |
| `--file` | `-f` | ホスト一覧の YAML ファイル、または NetBox の IP Addresses CSV エクスポートのパス | `""` |
| `--traceroute` | `-T` | 各ターゲットへの経路を表示し、10 分ごとに調べ直す (`-M` とは併用不可) | `false` |
| `--mtr` | `-M` | さらに全ホップへ毎秒プローブを送る MTR 方式 (`-T` とは併用不可) | `false` |
| `--dns-server` | `-d` | 名前解決に使用するカスタム DNS サーバー IP アドレス | `""` (OSデフォルト) |
| `--resolve-all` | | 宛先のホスト名を解決して得られたすべての IP を個別のターゲットとして並行監視する | `false` |
| `--discovery-mtu` | `-m` | IPv4 の DF プローブ、IPv6 の OS 推定値と Echo 確認で PMTU を調べる | `false` |
| `--interface` | `-I` | 使用するネットワークインターフェイス名 (例: `eth0`, `en0`)、送信元は宛先ファミリーごとに選択 | `""` |
| `--source` | `-S` | 送信元 IP アドレスの指定（IPv6 の zone 付きアドレスも可） | `""` (自動検出) |
| `--size` | `-s` | パケットのペイロードサイズ (バイト) | `56` |
| `--count` | `-c` | 各ターゲットに送信する回数 (0 は無制限) | `0` |
| `--duration` | | 指定時間後にアプリケーションを終了する（例: `30s`、`5m`、`1h30m`。0 は無制限） | `0s` |
| `--ipv4` | `-4` | IPv4 のみを使用する | `false` |
| `--ipv6` | `-6` | IPv6 のみを使用する | `false` |
| `--port` | `-p` | 疎通確認するポート (例: `443/tcp`, `53/udp`, `443`)。カンマ区切りで複数指定可 | `""` |
| `--web` | | Web UI を `http://127.0.0.1:<web-port>/` で提供する (このマシンからのみ接続可。[Web UI](web-ui.md) 参照) | `false` |
| `--web-port` | | `--web` の待ち受けポート (1–65535) | `8080` |
| `--asn` | `-a` | ターゲット IP の AS 番号を検索して表示する | `false` |
| `--ptr` | `-r` | ターゲット・ホップ IP の逆引き名を検索して表示する | `false` |
| `--http` | `-H` | ヘルスチェックする URL (例: `https://example.com/health`)。カンマ区切りまたは繰り返し指定で複数可 | `""` |
| `--check-config` | | 計測を始めずに有効な設定を検証・表示する（CLIのみ） | `false` |
| `--rtt-warn` | | RTT の warn 閾値 (ミリ秒・オレンジ) | `50` |
| `--rtt-crit` | | RTT の crit 閾値 (ミリ秒・赤) | `200` |
| `--jitter-warn` | | Jitter の warn 閾値 (ミリ秒・オレンジ) | `10` |
| `--jitter-crit` | | Jitter の crit 閾値 (ミリ秒・赤) | `50` |
| `--loss-warn` | | ロス率の warn 閾値 (パーセント・オレンジ) | `20` |
| `--loss-crit` | | ロス率の crit 閾値 (パーセント・赤) | `80` |

> **閾値 (Thresholds)** — `warn` がオレンジ、`crit` が赤の境界値で、LOSS% / RTT / JIT カラムの色分け (および Log ペインのアラート記録) に使われます。各メトリクスで `warn` は `crit` より小さい必要があります。YAML の `thresholds:` ブロックでも設定できます。

## 現在の状態判定と設定チェック

現在の状態と累積統計は分けて扱います。既定では **3回連続失敗で応答不能**、**2回連続応答で応答不能を解除**します。復旧後も直近のロス率・最新RTT・ジッターが閾値を超えていれば警告や異常を表示します。過去の平均・ピークRTTは現在の状態に影響しません。起動直後の1回のタイムアウトで異常にしないよう、直近ロス率の判定は `health-failures` 回以上の計測完了後に行います。一覧のLoss %は従来どおり、完了した計測に対する累積値です。

| オプション (YAMLでも同名のトップレベルキー) | 既定値 | 内容 |
|---|---|---|
| `--health-failures` | `3` | 応答不能と判定する連続失敗回数 (1–10000) |
| `--health-recoveries` | `2` | 応答不能を解除する連続応答回数 (1–10000) |
| `--health-window` | `1m` | 直近ロス率の集計期間 (1秒–1時間、1秒単位で集計)。期間を変更すると直近の集計窓を新しく開始 |
| `--http-follow-redirects` | `true` | 最大10回までリダイレクトを追う。falseなら最初の応答を判定 |

JSONの `health` には状態・理由・応答不能判定・連続回数・直近ロス率・最終計測時刻・最終応答時刻を含みます。直近の集計値は `evaluated_at` (最後の計測完了または判定ルールの変更時点) の窓を表し、一時停止中も判定理由と集計値をそろえて保持します。`measurement_state` (`running` / `finished` / `stopped`) はICMP計測の状態で、Webサーバーの `state` とは別です。TUIでは送信回数の上限到達後も補助監視を継続できます。Web の RTT グラフは実際の計測時刻を使い、一時停止中は空白として表示します。

JSONの各ターゲットには `levels` (`loss` / `last_rtt` / `jitter`) も含まれ、設定された閾値に対する判定を `none` / `ok` / `warn` / `crit` で表します。MTR の各ホップには `loss_level`、ポート・HTTP チェックには `level` (最初の結果が出るまでは `pending`) が付きます。TUI と Web UI はこの値で色分けします。

```bash
mping --check-config -f examples/monitoring.yaml --interval 500
```

`--check-config` (CLIのみ) は有効な設定、展開後の対象・対象数・グループとCLIで上書きした項目を出力します。計測・待ち受け・UI・出力ファイル作成は行わず、raw socketの権限も不要です。`--resolve-all` 指定時はDNS検索を行うことがあります。CLI指定はYAMLより優先されます。

HTTP チェックは 2xx / 3xx の応答を Up、それ以外を Down と判定します。URLは認証情報・フラグメントを含まないHTTP(S)のみ対応します。

状態変化はTUI・Webで共通の判定結果を使います。同一エラーの連続失敗は `count` と `last_at` を持つ1件のイベントに集約します。Web設定とセッションの終了期限は起動時の設定を維持し、変更時は再起動の案内を出します。ポート・ネットワーク設定の変更は測定を再構築して反映します。[設定例](../../examples/monitoring.yaml)も参照してください。

## シェル補完

mping は `mping --help` と同じフラグ定義から bash/zsh/fish 用の補完スクリプトを生成します。フラグ名、`-f` のファイルパス、`-I` のネットワークインターフェース名、`mping completion bash|zsh|fish` を補完できます。ファイルパスとインターフェース名は `--file=...` / `--interface=...` 形式や `-Ien` のような短縮フラグに続けて入力する形式にも対応します。スクリプト生成とインターフェース一覧取得に `sudo` は不要です。

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
