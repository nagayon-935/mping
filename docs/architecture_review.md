# mping アーキテクチャレビュー

- 調査日: 2026-07-09
- 対象コミット: `68215cd`（main ブランチ）
- 調査方法: 全ソース読解、`go vet` / `go build` / `go test -cover` / `deadcode`（golang.org/x/tools）実測、参照 grep

## 1. サマリ

mping は Go 製のマルチターゲット ping TUI ツール。**依存方向は完全に単方向で健全**、テストカバレッジも全パッケージ 77〜93% と高く、`go vet`・全テストがグリーンの状態にある。一方で、**`cmd/main/mping.go` の `run()`（約590行）と `internal/ui/tui.go` の `Run()`（約800行）の2つの神関数**にライフサイクル管理と描画制御が集中しており、また **pinger 層（traceroute/MTR プローブ）と UI 層（4種のモニタテーブル描画）に構造的な重複**が蓄積している。将来の変更（新モニタ種別の追加、設定項目の追加）のコストはこの2点に支配される。

## 2. 主要ディレクトリの役割

| パス | 行数(実装) | 役割 |
|---|---|---|
| `cmd/main/` | 1,555 | CLI ブートストラップ。フラグ解析、YAML マージ、ライフサイクル管理、リロードループ、traceroute オーケストレーション、JSON スナップショット書き出し |
| `internal/pinger/` | 2,110 | raw ICMP ソケット層。ping ワーカー/レシーバ、traceroute、MTR プローブプリミティブ、PMTU 探索、ポート/HTTP チェッカー、ASN ルックアップ（Team Cymru DNS） |
| `internal/stats/` | 999 | スレッドセーフな統計集計。`TargetStats`（ping）、`MTRStats`（hop 別）、`PortCheckResult`、`HTTPCheckResult`、JSON エクスポート |
| `internal/mtr/` | 264 | MTR エンジン。経路発見→継続プローブ→経路フラップ検出のオーケストレーション |
| `internal/ui/` | 3,084 | tview ベースの TUI。メインテーブル、RTT グラフ、traceroute/MTR/Port/HTTP モニタペイン、閾値色分け、狭幅端末対応 |
| `internal/watcher/` | 104 | fsnotify による設定ファイル監視（200ms デバウンス、rename-over 対応） |

テストコードは実装の約1.3倍（約11,000行）。テーブル駆動 + 関数変数注入によるモックで、**raw ソケットなしで（特権なしで）全テストが実行できる**設計は良好。

## 3. 主要モジュールの責務

### cmd/main/mping.go（単一ファイル 1,555 行）
1つのファイルに以下の責務が同居している:
- **設定**: `parseArgs`（pflag 定義）、`config` 構造体、`hostsFileYAML`/`thresholdsYAML`（YAML スキーマ）、`applyDocToCfg`（CLI 優先マージ）、`validateHostsDoc`
- **ネットワーク環境検出**: `getInterfaceIP` / `getInterfaceMTU` / `getPreferredOutboundIP` / `hasIPv6Connectivity` / `detectAutoSourceIPs`
- **ライフサイクル**: `run()`（mping.go:810–1402）が pinger / traceroute / MTR エンジン / PortChecker / HTTPChecker / watcher / JSON writer の起動・停止・リスタート・ホットリロードをローカルクロージャ群（`startPinger`, `stopPinger`, `stopAll`, `resetTrace`, `resetMTR`, `resetHTTP`, `resetPort`, `onFileChange`）で管理
- **アダプタ**: `pingerController`（12メソッドのインターフェース）+ `pingerAdapter` + `pingerMTRAdapter`
- **その他**: `runTraceroutes`、`expandTargets`（--resolve-all の展開）、`writeJSONSnapshot`、`printExitSummary`

### internal/pinger
- `pinger.go`: `Pinger` 本体。ICMP Echo ID をターゲットごとに割り当て（`baseID+i`）、per-target ワーカー goroutine + 共有レシーバ goroutine（v4/v6 統合ループ `runReceiver`）でリプライをチャネル分配。ASN ルックアップとキャッシュもここ。
- `traceroute.go`: `TraceRoute()`。TTL 制限プローブを送り、共有レシーバ経由（`traceChans`）または自前ソケットで受信。
- `mtr_probe.go`: `OpenHopSocket` / `ProbeHop` / `NextTraceID` — MTR エンジン用の1プローブ単位のプリミティブ。**traceroute.go とプローブ構築・受理判定・受信ロジックがほぼ二重実装**（§8 参照）。
- `pmtu.go` + `pmtu_{darwin,linux,other}.go`: DF ビット付きバイナリサーチによる PMTU 探索。macOS の `IP_DONTFRAG` 要件などプラットフォーム知見がコメントに集約されている。
- `portchecker.go` / `httpchecker.go`: TCP/UDP 到達性と HTTP ヘルスチェック。同型の Start/Stop/Wait/loop 構造。

### internal/stats
「ミューテックス保持の実体 + `GetView()` による読み取り専用スナップショット」パターンで統一。UI は必ず View 経由で読む。`MTRStats` は `TargetStats.mu` とのロック順序問題を避けるため自前ロックを持つ（stats.go:233 のコメントに明記）。

### internal/mtr
`HopProber` インターフェース（使用側定義）越しに pinger を叩く。discover（全 TTL 並行）→ probe（定期）→ rediscover（10分毎、フラップ検出）のループ。キャンセル時の「破れたラウンド」を丸ごと破棄する防御ロジックあり（engine.go:208–218）。

### internal/ui
- `tui.go`: `Run()`（74–871行）にウィジェット構築、キーバインド、フォーカスサイクル、リフレッシュループ、`updateTable` クロージャ（約180行）が集中。
- `tui_helpers.go`: セル整形、列幅フィット（`fitWidthsToAvailable`）、アラート状態遷移、コンパクト(2行)レイアウト構築。
- `graph.go` + `scale.go`: 自前 `tview.Box` サブクラスの RTT グラフ。オートスケール + ヒステリシス。
- `render_monitors.go` / `mtr_view.go` / `http_view.go`: traceroute・Port・MTR・HTTP の4テーブル描画（文字列組み立て）。`boxtable.go` に罫線ヘルパーを部分的に共通化。
- `thresholds.go`: 閾値。**パッケージグローバル変数** `activeThresholds`（setActiveThresholds/getActiveThresholds）で全描画関数に配られる。

## 4. 主要な処理の流れ

### 起動〜定常監視（代表ユースケース）
```
main → run(args)
  ├─ parseArgs (pflag)
  ├─ mergeHosts: YAML 読込 → applyDocToCfg（CLI フラグ優先）→ buildHostsAndGroups
  ├─ expandTargets (--resolve-all 時に DNS 展開して "host (ip)" 形式に変換)
  ├─ determineSourceIPs / getInterfaceMTU / hasIPv6Connectivity
  ├─ setupPMTU（-m 時: プローブ pinger で DiscoverMaxPayload）
  ├─ startPinger: pinger.Start(interval, timeout)
  │     ├─ per-target ワーカー goroutine（送信・応答待ち・統計更新）
  │     └─ 共有レシーバ goroutine（ICMP ID → targetChans[id] へ分配）
  ├─ (opt) mtr.Engine / PortChecker / HTTPChecker / watcher / JSON writer 起動
  └─ ui.Run(RunOptions)
        └─ refresh goroutine: interval/2 毎に QueueUpdateDraw(updateTable)
              updateTable → targets[i].GetView() → 列構築 → tview.Table 更新
                          → 各モニタペインの render*Table() で文字列再生成
```

### 統計データの流れ（書き手→読み手）
```
pinger ワーカー ──OnSuccess/OnFailure──→ stats.TargetStats（mu 保持）
mtr.Engine    ──RecordReply/RecordLoss─→ stats.MTRStats（自前ロック）
PortChecker   ──SetResult──────────────→ stats.PortCheckResult
HTTPChecker   ──SetResult──────────────→ stats.HTTPCheckResult
                                            │
UI refresh / JSON writer ←──GetView()（スナップショットコピー）──┘
```

### ホットリロード（YAML 変更 / a・d キー）
watcher（または OnAddHost/OnDeleteHost）→ `reloadRequested` セット → `reloadCh` close → TUI 停止 → `run()` の for ループ先頭に戻り、`cliCfg`（CLI 初期値）へ YAML を再適用してターゲット・全チェッカーを作り直す。**TUI ごと再構築する「全再起動」方式**で、状態は失われるが整合性は単純。

## 5. 依存関係の方向

```
cmd/main ──→ internal/pinger ──→ internal/stats
   │    ──→ internal/mtr    ──→ internal/{pinger,stats}
   │    ──→ internal/ui     ──→ internal/stats
   │    ──→ internal/watcher（依存なし・独立）
```

- **単方向・循環なし**。stats が最下層の共有語彙、pinger/ui が中間層、cmd/main が組み立て役という明確な層構造。
- ui は pinger を import しない（`HTTPResults func()` のような関数注入で結線）— 好ましい分離。
- インターフェースは使用側に定義（`mtr.HopProber`、cmd/main の `pingerController`・`tracer`）— Go の慣例に合致。ただし `pingerController` は12メソッドと大きく、「小さいインターフェース」原則からは逸脱（adapters の存在理由の大半はテスト差し替え）。

## 6. 共通処理の置き場所（Util/Shared の実態）

明示的な `util` パッケージは存在しない。事実上の共通処理置き場は:

| 置き場所 | 内容 | 課題 |
|---|---|---|
| `internal/stats` | View スナップショットパターン、`reconstructHistory`（リングバッファ復元） | RTT min/max/sum/リングバッファの記録ロジックが `TargetStats`・`PortCheckResult`・`HTTPCheckResult` で三重実装 |
| `internal/ui/tui_helpers.go`（715行） | 整形・色・幅計算・アラートの雑多な集積地 | 「ヘルパー」という名の何でも入れ。列インデックスのマジックナンバー（`shrinkOrder`, `2+offset` 等）が層をまたいで暗黙結合 |
| `internal/ui/boxtable.go` | 罫線テーブルの共通描画 | 4つのモニタ描画関数が部分的にしか使っておらず、full/compact 分岐・ステータス変化ログ検出が各所に再実装されている |
| `cmd/main/mping.go` 内 | ネットワーク検出系ヘルパー | main にしか置けない理由はなく、`internal/netutil` 等に切り出せる |

**層をまたぐ暗黙の共有知識**（コードでなく「文字列フォーマット」が共通処理になっている例）:
- `"host (ip)"` 表示形式: cmd/main の5箇所（mping.go:218, 907, 924, 1485, 1512）でパースし、ui の2箇所（tui.go:198, tui_helpers.go:210）で再構築判定する。型で表現されておらず、フォーマット変更が全層に波及する。
- `"write ip 0.0.0.0->"` エラーメッセージ書き換え: `pinger.applyLastErrSource`（pinger.go:189）と `ui.normalizeWriteIP`（tui_helpers.go:541）が同一ロジックを二重実装。

## 7. 実際に使われている設計パターン

| パターン | 実例 | 評価 |
|---|---|---|
| View スナップショット（イミュータブルな読み取りコピー） | `TargetStats.GetView()` ほか全 stats 型 | ◎ 一貫して適用。UI/export の並行安全性の要 |
| 関数変数注入によるテストダブル | `pinger.Options{ResolveIPAddr, ListenPacket, Now, LookupTXT}`、cmd/main の `newPinger`/`uiRun`/`interfaceByName` 変数 | ◎ 特権なしテストを実現。プロジェクト最大の強み |
| Adapter | `pingerAdapter` / `pingerMTRAdapter`（cmd/main） | ○ 動くが `pingerController` が肥大。境界の設計見直し余地 |
| Options 構造体 | `ui.RunOptions`（30フィールド弱）、`mtr.Config` + `withDefaults()` | △ RunOptions はコールバック8種を含み事実上の「何でも渡す袋」化 |
| 使用側インターフェース | `mtr.HopProber`, `HopSocket`, `tracer` | ◎ |
| プラットフォーム分岐（build tags） | `pmtu_{darwin,linux,other}.go` | ◎ 最小限の面積に閉じ込めている |
| パッケージグローバル状態 | `ui.activeThresholds` | △ テスト間干渉リスクと引き換えの利便性。現状は setActiveThresholds を Run 起動時1回に限定して管理 |

MVC/Clean Architecture のような命名は使っていないが、実態は「stats をドメインモデルとした Humble View + ワーカー」構成の独自パターンで、方向性は一貫している。

## 8. 設計上の一貫性が崩れている箇所

1. **traceroute と MTR プローブの二重実装** — `traceroute.go:19–265` の `TraceRoute` は、`mtr_probe.go` の `ProbeHop`/`acceptHopPacket`/`receiveViaTraceChan`/`receiveViaSocket` と同じ「プローブ構築 → traceChan 登録 → 受理判定 → タイムアウト」ロジックを独自に持つ。trace ID 生成式もインライン（traceroute.go:79）と `NextTraceID()`（mtr_probe.go:78）で重複。歴史的に traceroute が先、MTR が後から整理されて追加されたことが読み取れる。
2. **エラーログ追記関数が2系統** — `appendErrorLog`（tui_helpers.go:649、上限超過時に1行捨てて全文再構築）と `appendErrorLogRaw`（http_view.go:220、上限超過時にスライス切り詰めのみで TextView は再構築しない）。エビクション挙動が微妙に異なり、HTTP ペイン経由のログだけ TextView 側が無限に伸びる。
3. **4つのモニタ描画関数の構造重複** — `renderTracerouteTable` / `renderPortMonitorTable` / `renderMTRTable` / `renderHTTPMonitorTable` はいずれも「full/compact 判定 → 列幅計算 → boxBorder/ヘッダ → 行ループ」+（Port/HTTP は）「ステータス変化検出 → ログ追記」を各自実装。`boxtable.go` の共通化は罫線までで止まっている。
4. **列レイアウトの暗黙結合** — メインテーブルは `fullHeaders`/`baseWidths`/`minWidths`/`maxWidths`/`fullAligns` の並行スライスで管理され、DNS/ASN 列の有無で位置がずれる。ところが `fitWidthsToAvailable` の `shrinkOrder`/`growOrder`（tui_helpers.go:384, 405）は13列固定のインデックス列、`buildFullRowCells` は `case 2+offset` 式のオフセット計算（tui_helpers.go:610–625）、`updateTable` 内の `dynamicCols` は ASN のみ考慮して DNS を無視（tui.go:443–446、`calcColumnWidths` は両方考慮しており不整合）。「列」という概念が構造体になっていないことが根本原因。
5. **閾値適用経路が2系統** — YAML 閾値の適用が `applyThresholdsDoc`（cfg の ms int へ、mping.go:408）と `overlayThresholds`（`ui.Thresholds` へ、mping.go:532、validate 用）の2実装。
6. **RFC 1889 ジッタ計算の重複** — stats.go:354–361（TargetStats）と mtr.go:109–115（HopStats）。
7. **RTT 記録（min/max/sum/リングバッファ）の三重実装** — stats.go:55（Port）、httpcheck.go:77（HTTP）、stats.go:348–386（Target）。

## 9. 肥大化している箇所

| 箇所 | 規模 | 内容 |
|---|---|---|
| `cmd/main/mping.go run()` | mping.go:810–1402（約590行） | 設定確定・全コンポーネント起動/停止・リロードループ・8つのローカルクロージャ・共有ミューテックス2本（`pMu`, `reloadMu`）。ロック規約（「Caller must hold pMu」等）がコメント頼み |
| `internal/ui/tui.go Run()` | tui.go:74–871（約800行） | ウィジェット構築（5ペイン × ほぼ同型の初期化コード）・キーハンドラ・`updateTable` クロージャ・リフレッシュ goroutine・レイアウト組み立て |
| `internal/ui/tui_helpers.go` | 715行 | §6 のとおり雑多な集積地 |
| `cmd/main/mping.go` の設定配管 | 約250行 | フラグ定義（parseArgs）・`config`・`hostsFileYAML`・`applyDocToCfg`（16個の同型 if）・`validateHostsDoc` の5点セット。**設定項目を1つ足すたびに最低5箇所の同期が必要** |
| テスト: `internal/ui/tui_test.go` | 2,800行 | 単一ファイルに UI テストが集中（機能別分割の余地） |

パフォーマンス面の軽微な無駄: `updateTable` は毎 tick、pass 1（アラート判定）と pass 2（描画）で `buildFullColumns`＋`GetView()` を各ターゲット2回ずつ呼ぶ（tui.go:538 と 561/578）。ターゲット数十件では実害なし。

## 10. 変更に対する耐性評価と、リファクタリング時の注意点

**耐えられる変更**: stats への統計項目追加、モニタペインの表示調整、pinger のエラー種別追加 — View パターンとテスト網が守ってくれる。

**高コストな変更**: ①設定項目の追加（5点セット同期）、②新しいモニタ種別の追加（チェッカー + stats 型 + RunOptions コールバック3種 + render 関数 + run() 配線をすべて手書き）、③メインテーブルの列変更（並行スライス+マジックインデックスの同期）。

### 注意点（変更時の地雷）
1. **並行性の中核（pinger のワーカー/レシーバ、`run()` の停止順序）には安易に触れない。** 停止順序は「traceroute join → MTR stop → pinger Stop/Wait → port/http Stop/Wait → watcher cancel/join → reload 状態読み取り」と厳密に並んでおり、順序変更は shutdown レースを生む。直近コミット（9ee0dce の torn discover 対策等）もこの領域のレース修正であり、既知の落とし穴が多い。
2. **`stats` の2種類のロック（TargetStats.mu と MTRStats 自前ロック）のロック順序規約**を崩さない（stats.go:233 コメント参照）。
3. **ICMP Echo ID 空間の使い分け**（ping: `baseID+i`、trace/MTR: `baseID+0x1234+counter`、PMTU: `baseID`）は暗黙の割り当て規約。統合時に衝突させない。
4. **`ui.Run` はテストが挙動を厳密に固定している**（tui_test.go 2,800行）。描画文字列レベルのスナップショット的アサーションが多く、無害なリファクタでもテスト修正が大量に出る。分割は「テストと同時に」計画すること。
5. プラットフォーム分岐（pmtu_*）は darwin/linux/other の3系統すべての確認が必要（CI は ubuntu のみ = darwin パスは CI で未検証）。
