# 現在の構成

既存のパッケージ境界を維持し、2026-10-29 に CLI、監視、統計、TUI、Web、レポートの責務を整理した。

## 処理の流れ

1. `cmd/main` が CLI を読み、`internal/hostsfile` が設定ファイルを読んでホストパターン・include（NetBox CSV を含む）を展開する。`cmd/main` はその結果を設定に適用し、検証する。
2. 起動時のネットワーク設定・プローブ構築を経て、supervisor が監視の開始、停止、再読み込みを管理する。
3. `internal/pinger` と `internal/mtr` が計測し、`internal/stats` に結果を記録する。
4. TUI、Web、レポートは統計のスナップショットを読み、表示・出力する。
5. `--web` のとき、ブラウザ向けの部分は別プロセス（`mping __web`）で動く。このプロセスは起動直後に権限を恒久的に捨て、本体の API には専用ディレクトリ内の UNIX ソケットで接続する。
6. `internal/watcher` は設定ファイル変更を通知し、`internal/privilege` は呼び出し元ユーザー権限でのファイル操作と、Web プロセスの恒久的な権限放棄を支える。

## 責務と変更先

| 領域 | 主なファイル | 責務 |
|---|---|---|
| CLI | `cmd/main/mping.go` | アプリケーションの起動手順と UI 接続 |
| 設定ファイル | `internal/hostsfile` | YAML 文書の構造、ホストパターン展開、include・NetBox CSV の読み込み。ファイルはすべて起動ユーザーの権限で読む |
| 設定 | `cmd/main/config.go`, `config_validation.go` | 設定の適用と意味的検証を分離 |
| 起動準備 | `cmd/main/startup_network.go`, `startup_probes.go` | ネットワーク選択・DNS・送信元とプローブの構築 |
| CLI 出力 | `cmd/main/output.go` | 終了サマリーと終了コード |
| Web プロセス | `cmd/main/web_process.go`, `web_process_child.go` | 本体側で API のソケットを用意して Web プロセスを起動・停止し、子側で権限を捨ててから配信する。Linux では `no_new_privs` を付けて起動する |
| 監視 | `internal/pinger/periodic.go` | HTTP・ポート監視の共通定期実行。プロトコル固有の処理は各 checker に保持 |
| 統計 | `internal/stats/stats.go`, `view.go`, `export.go` | 状態更新、読み取り用ビュー、JSON 向け変換を分離 |
| 閾値 | `internal/stats/thresholds.go` | 閾値と判定レベル。TUI・Web・JSON・health はすべてここで判定し、表示側はレベルを色に写すだけにする |
| TUI | `internal/ui/graph.go`, `graph_layout.go` | グラフの描画・入力と配置・履歴投影を分離 |
| Web | `internal/web/backend.go`, `frontend.go`, `api.go`, `snapshot.go` | 本体側の API（token の確認を含む）と、ブラウザ向けの部分（Host/Origin 検査・静的ファイル・SSE・API の中継）を分離。`web.Start` は両者を 1 プロセスで組み合わせ、テストと開発用サーバーが使う |
| レポート | `internal/report/report.go`, `csv.go`, `write.go` | データ構造、CSV 整形、安全なファイル公開を分離 |

レポートと Web の JSON スナップショットは `stats.BuildSnapshot` を共有する。出力に不要なターゲット RTT 履歴をコピーせず、必要な統計値だけを既存のビュー経由で取得する。

## 維持する性質

- 削除した設定（フラグ・YAML キー）は黙って無視せず、削除済みであることを示すエラーにする。
- periodic runner は初回チェック完了後に ticker を作成し、以降のチェックを直列実行する。キャンセルされた計測を失敗として記録しない処理は各 checker が担当する。
- ビュー生成は従来のロック範囲を保持する。スナップショットキャッシュの generation/version 判定も維持する。
- レポート保存の排他的な作成、所有者設定、同期、公開順序を維持する。
- NetBox の名前は全 include 展開後に一意化し、手動指定名を保持する。

## 検証

```sh
go test -race ./...
go vet ./...
GOOS=windows GOARCH=amd64 go vet ./...
staticcheck ./...
node --test internal/web/jstest/*.test.mjs
GOOS=linux GOARCH=amd64 go build ./...
make integration   # root 権限で実 ICMP を送る結合テスト
```

ネットワーク関連テストは localhost の TCP/UDP ソケットを使用するため、bind を許可した環境で実行する。単体テストは raw ソケットをすべて差し替えており、実際の ICMP 送受信（TTL の制御メッセージを含む）は `-tags integration` の結合テストだけが通す。CI は Linux 上でこれを root で実行する。Linux・Windows 向けのクロスビルドと vet はコンパイル検証であり、Linux 上の実行テストは CI が担当する。Windows 上の実行テストはない。
