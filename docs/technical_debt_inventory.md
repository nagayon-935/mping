# mping 技術的負債インベントリ

- 調査日: 2026-07-09 / 対象コミット: `68215cd`
- 根拠: 全ソース読解 + `go vet`（クリーン）/ `go test -cover`（全緑: cmd/main 82.7%, mtr 83.3%, pinger 79.8%, stats 92.9%, ui 89.8%, watcher 77.1%）/ `deadcode` / 参照 grep
- 未使用コード・リソースの削除系は [deletion_candidates.md](./deletion_candidates.md)、全体戦略は [refactoring_master_plan.md](./refactoring_master_plan.md) を参照。

**凡例** — 優先度: 高/中/低、リスクレベル（放置した場合の影響度）: 致命的/高/中/低。
「LLM への依頼単位」は GPT-5.5 / Claude Opus 4.7 等に1プロンプトで依頼できる粒度を示す。

---

## 分類1: すぐ直すべき負債

### TD-01 コミット済みビルド成果物 `main`（11.8MB）が git 追跡されている — ✅ done (PR #41)
- **問題の概要**: `go build` の成果物と思われる 11.8MB のバイナリ `main` がリポジトリにコミットされている（コミット `2ae5535` で混入）。`.gitignore` は `mping` は無視するが `main` を無視していない。
- **該当ファイル**: リポジトリルート `main`、[.gitignore](../.gitignore)
- **なぜ問題か / 放置リスク**: clone サイズの恒久的肥大（git 履歴に残る）、macOS ローカルビルドのバイナリを他プラットフォームのユーザーが誤って掴む危険、セキュリティレビュー対象の増加。
- **修正方針（ゴール指標）**: `git rm --cached main` + `.gitignore` に `main` を追加。`git ls-files | grep -E '^(main|mping)$'` が空になること。（履歴からの完全除去 = filter-repo は公開リポジトリのため実施しない判断も可。その場合は理由を PR 説明に記録）
- **想定される影響範囲**: なし（コードから参照されていない）。
- **優先度: 高 / リスクレベル: 中**
- **LLM への依頼単位**: 1プロンプト。「`main` を git 追跡から外し .gitignore に追加、`git status` と `git ls-files` の確認結果を提示せよ」。

### TD-02 CI に `-race` / `go vet` / lint / フォーマットチェックがない — ✅ done (PR #41)
- **問題の概要**: CI（[ci.yml](../.github/workflows/ci.yml)）は `go test -v -coverprofile` のみ。並行処理が中核のプロジェクトなのに race detector が回っておらず、`go vet`・`gofmt -l`・`staticcheck`（または golangci-lint）・`govulncheck` も未実施。
- **該当ファイル**: `.github/workflows/ci.yml`
- **なぜ問題か / 放置リスク**: このコードベースの直近の修正履歴はレース・shutdown 順序系（`9ee0dce` 等）に集中しており、race detector なしの CI は再発を検出できない。**以降のリファクタリング全フェーズの安全網になるため最優先。**
- **修正方針（ゴール指標）**: CI に `gofmt -l`（差分ゼロ確認）→ `go vet ./...` → `go test -race -coverprofile ./...` → `staticcheck ./...`（または golangci-lint）→ `govulncheck ./...` を追加。macOS ジョブ（`runs-on: macos-latest`、ビルド+テストのみで可）を追加して `pmtu_darwin.go` 系統をカバー。
- **想定される影響範囲**: CI 定義のみ。ただし `-race` 化で既存テストの潜在レースが露見する可能性あり（露見したらそれ自体が成果）。
- **優先度: 高 / リスクレベル: 高**
- **LLM への依頼単位**: 2プロンプト。①ci.yml 拡張（-race, vet, gofmt, macOS ジョブ）、②lint 導入（staticcheck/golangci-lint 選定と指摘のトリアージ。指摘の修正は別プロンプトに分割）。

### TD-03 `updateTable` の動的列処理が DNS 列を無視（`calcColumnWidths` と不整合） — ✅ done (PR #41)
- **問題の概要**: [tui.go:443–446](../internal/ui/tui.go) の `dynamicCols := []int{0,1}; if asnEnabled { append(dynamicCols, 2) }` は、`dnsEnabled` の場合に列2が DNS 列になることを考慮していない（同ファイルの `calcColumnWidths`（tui.go:176–215）は DNS/ASN 両方を正しくハンドリングしており不整合）。DNS 有効時は動的最大幅の引き上げが誤った列に適用される。
- **該当ファイル**: `internal/ui/tui.go`
- **なぜ問題か / 放置リスク**: `-d`（dns-server）使用時に列幅の伸長が DNS/ASN 列で崩れ、長いホスト名や ASN 表示が不必要に切り詰められる。実害は表示品質のみだが、TD-21（列モデル再設計）の前に現行挙動の正解を固定しておかないと、リファクタ時に「バグを仕様として保存」してしまう。
- **修正方針（ゴール指標）**: `calcColumnWidths` と同じ dnsEnabled/asnEnabled 分岐に揃える。dns×asn の4通り組み合わせのテーブル駆動テストを追加。
- **想定される影響範囲**: `updateTable` 内の数行 + テスト追加。
- **優先度: 高 / リスクレベル: 低**
- **LLM への依頼単位**: 1プロンプト（修正 + 4通りのテスト）。

### TD-04 HTTP ペインのログ追記関数のエビクション不整合 — ✅ done (PR #41)
- **問題の概要**: `appendErrorLog`（[tui_helpers.go:649](../internal/ui/tui_helpers.go)）は上限（1000行）超過時にログスライスと TextView を同期再構築するが、HTTP ペイン専用の `appendErrorLogRaw`（[http_view.go:220](../internal/ui/http_view.go)）はスライスだけ切り詰めて TextView には追記し続ける。長時間運用で HTTP ステータス変化が多いと TextView 側のメモリ・描画コストが際限なく増える。
- **該当ファイル**: `internal/ui/http_view.go`, `internal/ui/tui_helpers.go`
- **なぜ問題か / 放置リスク**: 長期監視（このツールの主用途）でのメモリリーク相当。二重実装自体が変更漏れの温床。
- **修正方針（ゴール指標）**: `appendErrorLogRaw` を廃止し `appendErrorLog` に一本化（シグネチャ差異は `io.Writer` 化か `*tview.TextView` 化で吸収）。エビクション動作のテストを1本追加。
- **想定される影響範囲**: http_view.go の呼び出し1箇所 + render のテスト。
- **優先度: 高 / リスクレベル: 中**
- **LLM への依頼単位**: 1プロンプト。

---

## 分類2: 次の改修時に一緒に直すべき負債

### TD-05 `"write ip 0.0.0.0->"` 書き換えロジックの層またぎ重複
- **✅ 調査済み（refactor/phase-2, 2026-07-10）: 削除は見送り。** `pinger.applyLastErrSource` は `p.Source != ""`（`-S`/`-I` 明示指定時）のみ動作し、`ui.normalizeWriteIP` は自動検出モード（`p.Source` が空でバインドは `0.0.0.0` だが、UI 側は `detectAutoSourceIPs` で検出済みの表示用送信元 IP を持つ）をカバーする。両者は同じ文字列置換ロジックを持つが**担当するケースが排他的**であり、`tui_test.go:512` は自動検出シナリオを直接検証している。ドキュメント初版の「UI側を削除」という推奨は誤りで、実施すると自動検出時（デフォルト・最頻ケース）にエラーメッセージが `0.0.0.0` のまま表示される回帰を招く。両関数を維持し、意図を明示するコメントを追加した（pinger.go, tui_helpers.go）。
- **概要（当初の記載、参考）**: `pinger.applyLastErrSource`（[pinger.go:192](../internal/pinger/pinger.go)）と `ui.normalizeWriteIP`（[tui_helpers.go:560](../internal/ui/tui_helpers.go)）が同一の文字列置換を実装。
- **優先度: 中 / リスクレベル: 低 → 対応不要と判明**
- **LLM 依頼単位**: 1プロンプト。

### TD-06 RFC 1889 ジッタ計算の二重実装 — ✅ done (PR #41)
- **概要**: [stats.go:354–361](../internal/stats/stats.go)（TargetStats.OnSuccess）と [mtr.go:109–115](../internal/stats/mtr.go)（MTRStats.RecordReply）に同じ平滑化式（`jitter += (delta - jitter) / 16`）が2つ。
- **修正方針**: stats パッケージ内に `updateJitter(current, last, rtt) int64` 等の非公開ヘルパーを切り出して両者から呼ぶ。既存テスト（stats_test.go / mtr_test.go）は変更なしで通ること。
- **優先度: 中 / リスクレベル: 低** / **LLM 依頼単位**: 1プロンプト（TD-07 と同時依頼可）。

### TD-07 RTT 記録（min/max/sum/samples + リングバッファ）の三重実装 — ✅ done (PR #41)
- **概要**: `PortCheckResult.recordRTT`（[stats.go:55](../internal/stats/stats.go)）、`HTTPCheckResult.recordRTT`（[httpcheck.go:77](../internal/stats/httpcheck.go)）、`TargetStats`（OnSuccess 内のインライン + `appendHistory`）が同型ロジックを個別に持つ。
- **修正方針**: `rttAccumulator`（min/max/sum/samples/ring buffer を持つ非公開構造体）を stats に導入し3箇所を置換。`GetView` の出力（公開 View 構造体）は不変とする。
- **影響範囲**: stats パッケージ内部のみ。View 構造体・JSON 出力は変更しない（ゴール指標: export_test.go が無変更で通る）。
- **優先度: 中 / リスクレベル: 低** / **LLM 依頼単位**: 1プロンプト。

### TD-08 trace ID 生成式の重複 — ✅ done (PR #41)
- **概要**: [traceroute.go:79](../internal/pinger/traceroute.go) のインライン式 `(p.baseID + 0x1234 + int(p.traceCounter.Add(1))) & 0xffff` と [mtr_probe.go:78 `NextTraceID()`](../internal/pinger/mtr_probe.go) が同一。ICMP ID 空間の割り当て規約（ping: `baseID+i` / trace: `baseID+0x1234+counter` / PMTU: `baseID`）がコードに散在。
- **修正方針**: traceroute.go 側を `p.NextTraceID()` 呼び出しに置換し、ID 空間の割り当て規約を `NextTraceID` のコメントに集約。
- **優先度: 中 / リスクレベル: 低** / **LLM 依頼単位**: 1プロンプト（TD-20 の前哨戦として単独実施可）。

### TD-09 `updateTable` が毎 tick `GetView()`+`buildFullColumns` を各ターゲット2回呼ぶ — ✅ done (PR #41)
- **概要**: pass 1（アラート判定、[tui.go:525–547](../internal/ui/tui.go)）と pass 2（描画、tui.go:550–584）で同じスナップショット取得と列構築を繰り返す。pass 1 の `cols` は `_ = cols` で捨てられている。
- **修正方針**: pass 1 で `view`/`cols`/`lossRate` をスライスに保持して pass 2 で再利用。ゴール指標: 1 tick あたりの `GetView()` 呼び出しがターゲットあたり1回。
- **優先度: 低 / リスクレベル: 低**（数十ターゲットでは実害なし。updateTable 分割（TD-21）と同時に解消するのが効率的） / **LLM 依頼単位**: 1プロンプト。

### TD-10 閾値 YAML 適用ロジックの二重実装 — ✅ done (PR #41)
- **概要**: `applyThresholdsDoc`（[mping.go:408](../cmd/main/mping.go)、cfg の int/float フィールドへ）と `overlayThresholds`（mping.go:532、`ui.Thresholds` へ、バリデーション用）が同じ6項目のオーバーレイを別表現で実装。
- **修正方針**: cfg 内の閾値表現を `ui.Thresholds` に統一し（ms int フィールド廃止）、オーバーレイ関数を1つに。フラグ定義は `IntVar` のまま受けて構築時に変換。
- **影響範囲**: config 構造体、parseArgs、thresholdsFromCfg、関連テスト。TD-19（設定配管の再設計）に内包可能。
- **優先度: 中 / リスクレベル: 低** / **LLM 依頼単位**: 1プロンプト。

### TD-11 graph.go の痕跡コード — ✅ done (PR #41)
- **概要**: [graph.go:431–432](../internal/ui/graph.go) の `_ = gy100` / `_ = totalSteps` は過去の実装の名残（gridSteps 経由で使用済みのため不要な変数受け）。
- **修正方針**: `gridStepsForHeight` の戻り値設計を整理（使わない値を返さない）。
- **優先度: 低 / リスクレベル: 低** / **LLM 依頼単位**: TD-09 等 UI 内の他修正と同一プロンプトに同梱。

### TD-12 CLAUDE.md のアーキテクチャ記述が実態と乖離 — ✅ done (PR #41)
- **概要**: [CLAUDE.md](../CLAUDE.md) は `traceroute.go` / `httpchecker.go` / `portchecker.go` / `pmtu_*.go` をリポジトリ直下のように記載しているが、実際は `internal/pinger/` 配下。
- **リスク**: 以降の LLM セッションが誤った前提でファイルを探す。
- **修正方針**: CLAUDE.md の該当行を `internal/pinger/` 配下として書き直す。docs/ 4文書への参照も追記。
- **優先度: 中 / リスクレベル: 低** / **LLM 依頼単位**: 1プロンプト（5分作業）。

---

## 分類3: 影響範囲が大きいため設計判断が必要な負債

### TD-19 設定配管の5点セット同期問題（フラグ/config/YAML/apply/validate） — ✅ done (PR #41)
- **概要**: 設定項目を1つ追加するには `parseArgs` のフラグ定義、`config` フィールド、`hostsFileYAML` タグ、`applyDocToCfg` の if ブロック（現在16個、[mping.go:344–404](../cmd/main/mping.go)）、`validateHostsDoc` の5箇所を同期修正する必要がある。
- **リスク**: 追加漏れ（YAML だけ効かない設定項目）はコンパイルエラーにならず、テストを書き忘れると発見されない。
- **設計判断が必要な点**: ①リフレクション/ジェネリクスで宣言的にするか（複雑さと引き換え）、②`applyDocToCfg` を「項目定義テーブル + 汎用適用ループ」に再編するか、③現状維持で「設定追加チェックリスト」をテストで強制するか（`hostsFileYAML` のフィールド数と適用処理の対応を reflect で検査するメタテスト）。**推奨は③→②の段階導入**（YAGNI）。
- **修正方針（ゴール指標）**: 新設定項目の追加が「2箇所以下の修正 + テスト」で完了する状態。
- **影響範囲**: cmd/main 全域、mping_test.go の設定系テスト。
- **優先度: 中 / リスクレベル: 中**
- **LLM 依頼単位**: 2プロンプト。①メタテスト導入（現状の対応漏れ検査）、②適用ループへの再編（メタテストを安全網にして実施）。

### TD-20 `TraceRoute` と MTR プローブ経路の二重実装の統合 — ✅ done (PR #41)
- **概要**: [traceroute.go](../internal/pinger/traceroute.go)（265行）は `ProbeHop`/`acceptHopPacket`/`receiveViaTraceChan`/`receiveViaSocket`（[mtr_probe.go](../internal/pinger/mtr_probe.go)）とほぼ同じプローブ送受信ロジックを独自に持つ。`acceptPacket`（traceroute.go:94–123）と `acceptHopPacket`（mtr_probe.go:202–221）は判定内容が同一。
- **設計判断が必要な点**: `TraceRoute` を「`OpenHopSocket` + TTL ループで `ProbeHop` を順次呼ぶ」形に書き換えるのが自然だが、微妙な挙動差がある — ①traceroute は1ソケットを全 TTL で使い回す（ProbeHop と同じなので問題なし）、②traceroute の ASN 注釈付与（`"ip(ASxxx)"` 形式）は呼び出し側へ移す必要、③ctx キャンセル時の返り値（エラー vs 部分結果）の仕様確定、④ソケットフォールバックパスのタイムアウト挙動差。統合後は traceroute_errors_test.go / pinger_test.go の trace 系 + mtr_probe_test.go が全部通ることが最低ライン。
- **修正方針（ゴール指標）**: traceroute.go が 100 行以下になり、受理判定・受信ループの実装が mtr_probe.go の1系統のみになる。
- **影響範囲**: internal/pinger の2ファイル + cmd/main の `runTraceroutes`（表示形式が変わる場合）。**raw ソケット実機での動作確認（macOS/Linux 両方）が必須**（モックでは受信タイミングの差が見えない）。
- **優先度: 中 / リスクレベル: 高**（ネットワーク I/O の挙動変化リスク）
- **LLM 依頼単位**: 3プロンプト。①現挙動の特性テスト追加（両実装の受理判定を同一ケース群で固定）、②acceptPacket 統合、③TraceRoute 本体の ProbeHop ベース書き換え。各ステップで実機 `sudo ./mping -T 8.8.8.8` / `-M 8.8.8.8` の手動確認を挟む。

### TD-21 UI 列レイアウトの並行スライス+マジックインデックス構造 — ✅ done (PR #41)
- **概要**: メインテーブルの列定義が5本の並行スライス（headers/aligns/base/min/max）で、`shrinkOrder`/`growOrder`（[tui_helpers.go:384,405](../internal/ui/tui_helpers.go)）は13列固定インデックス、セル色付けは `case 2+offset`（tui_helpers.go:610–625）。DNS/ASN 列の有無で全インデックスがずれる構造で、TD-03 のようなバグを構造的に生む。
- **設計判断が必要な点**: `column` 構造体（name/align/min/max/priority/render(view)→string/color(view)→Color）のスライスへの全面再編。`fitWidthsToAvailable` の shrink/grow 優先度も column のフィールドに移す。**tui_test.go（2,800行）の期待値が大量に書き換わるため、描画結果のゴールデン（文字列スナップショット）を先に固定してから実施する**のが安全。
- **修正方針（ゴール指標）**: 列追加・削除が column 定義1箇所の変更で済む。DNS/ASN 4通り組み合わせで描画ゴールデンが一致。
- **影響範囲**: tui.go / tui_helpers.go / tui_test.go の広範囲。
- **優先度: 中 / リスクレベル: 中**
- **LLM 依頼単位**: 3プロンプト。①4通り組み合わせのゴールデンテスト整備、②column 構造体導入と calcColumnWidths/fitWidths の移行、③buildFullRowCells/updateTable の移行と旧並行スライス削除。

### TD-22 `run()`（590行）のライフサイクル管理の分解 — ✅ done (PR #41)
- **概要**: [mping.go:810–1402](../cmd/main/mping.go)。アーキテクチャレビュー §9 参照。8つのクロージャと2本のミューテックス（`pMu`, `reloadMu`）による暗黙のロック規約が最大の危険源。
- **設計判断が必要な点**: ①`supervisor` 構造体（pinger/trace/mtr/port/http の起動・停止・リセットを所有、pMu を内包）と ②`reloadCoordinator`（reloadMu/reloadRequested/reloadDoc/reloadNewHosts を所有）の2型への分離を推奨。コールバック群（OnStop/OnRestart/OnReset*）は supervisor のメソッド参照になる。**停止順序（trace join → mtr → pinger → port/http → watcher）は仕様として単体テストに固定してから移行する。**
- **修正方針（ゴール指標）**: `run()` が 150 行以下（パース→構築→ループ制御のみ）。stop 順序テストと reload シーケンステストが存在する。
- **影響範囲**: cmd/main 全体、mping_test.go の run 系テスト（1,893行の相当部分）。
- **優先度: 中 / リスクレベル: 高**（shutdown レースの再導入リスク）
- **LLM 依頼単位**: 4プロンプト。①停止順序・リロードの特性テスト追加、②supervisor 抽出（挙動不変）、③reloadCoordinator 抽出、④run() 本体の整理。各ステップ `go test -race ./cmd/...` 必須。

### TD-23 `ui.Run()`（800行）の分解 — ✅ done (PR #41)
- **概要**: [tui.go:74–871](../internal/ui/tui.go)。5つのモニタペイン構築が同型コードの繰り返し（trace/mtr/port/http で各20行 × 4）、キーハンドラ・リフレッシュループ・レイアウトが1関数に同居。
- **設計判断が必要な点**: ①`monitorPane` 構造体（view/pane/setBorderColor/title/enabled/render func）を導入して4ペインをデータ駆動化、②キーハンドラを `newInputHandler(deps)` として分離、③updateTable を `tableRenderer` 型に。TD-21（列モデル）と順序調整が必要 — **推奨は TD-21 → TD-23**。
- **修正方針（ゴール指標）**: Run() が 200 行以下。ペイン追加が monitorPane 定義の追加1箇所で済む。
- **影響範囲**: internal/ui 全域 + tui_test.go。
- **優先度: 中 / リスクレベル: 中**
- **LLM 依頼単位**: 3プロンプト。①monitorPane データ駆動化、②キーハンドラ分離、③リフレッシュループ/updateTable の型化。

### TD-24 `"host (ip)"` 文字列プロトコルの型化
- **概要**: `--resolve-all` が生成する `"host (ip)"` 表示文字列が事実上のデータフォーマットになっており、cmd/main の5箇所（[mping.go:218, 907, 924, 1485, 1512](../cmd/main/mping.go)）でパース、ui の2箇所（tui.go:198, tui_helpers.go:210）で判定される。
- **設計判断が必要な点**: `stats.TargetStats` に `DisplayName`/`ResolvedIP` を分離して持たせるか、cmd/main に `targetSpec{Host, PinnedIP}` 型を導入して stats へは確定値だけ渡すか。後者が層の責務的に自然だが、ホスト重複判定（OnAddHost）やリロード時のホストリスト（`currentHosts []string`）の型も変わるため波及が広い。
- **修正方針（ゴール指標）**: `strings.Index(x, " (")` によるパースが全コードから消える。
- **影響範囲**: cmd/main、ui の表示系、関連テスト。
- **優先度: 低〜中 / リスクレベル: 中** / **LLM 依頼単位**: 2プロンプト（型導入と cmd/main 置換 → ui 側置換）。

### TD-25 YAML ホットリロードで `port:` 変更が無視される — ✅ done (PR #41)
- **概要**: ポート指定は起動時に1回だけパースされ（[mping.go:873–882](../cmd/main/mping.go) のコメント「port changes require a process restart」）、リロードループの外にある。YAML で `port:` を書き換えても反映されず、**エラーも警告も出ない**。
- **リスク**: ユーザーは「設定は watcher が自動反映」（CLAUDE.md にも記載）と信じており、静かな不整合になる。
- **設計判断が必要な点**: ①リロードループ内に取り込んで完全対応するか、②リロード時に差分検出して「port の変更には再起動が必要」と Log ペインに警告するか。②が低リスクで UX 問題を解消する。
- **優先度: 中 / リスクレベル: 中** / **LLM 依頼単位**: 1プロンプト（②なら小規模。①なら TD-22 完了後に）。

### TD-26 watcher エラー後にホットリロードが静かに死ぬ
- **概要**: `watcher.Watch` は fsnotify エラーで return し（[watcher.go:90–97](../internal/watcher/watcher.go)）、呼び出し側（[mping.go:1203–1212](../cmd/main/mping.go)）はログを1行出すだけで再起動しない。以降 YAML 変更は無反応。
- **設計判断が必要な点**: バックオフ付き再起動ループを cmd/main 側に持つか、watcher パッケージに `WatchWithRetry` を追加するか。TUI Log への「auto-reload disabled」明示だけでも UX は改善する。
- **優先度: 低 / リスクレベル: 中**（fsnotify エラーは稀） / **LLM 依頼単位**: 1プロンプト。

---

## 分類4: 直さなくてもよい、または優先度が低い負債

### TD-30 `pingerController` インターフェースの肥大（12メソッド）
[mping.go:40–52](../cmd/main/mping.go)。テスト差し替えのためのインターフェースで、実害はない。TD-22 の supervisor 抽出時に自然に再分割されるため、単独では着手しない。

### TD-31 `coverage_extra_test.go` / `coverage_hop_test.go` という命名
カバレッジ目的のテストであることが名前から露骨（[internal/pinger/](../internal/pinger/)）。テスト自体は有効なので、機能ベースの名前（`asn_extra_test.go` 等）への改名は次にファイルを触るときで十分。

### TD-32 `hasIPv6Connectivity` が起動時に Cloudflare へ実 UDP dial
[mping.go:201–212](../cmd/main/mping.go)。ハードコードされた外部 IP（`2606:4700:4700::1111`）への依存だが、失敗時は IPv4 にフォールバックするだけで害はない。オフライン/閉域網では常に ip4 になる、という挙動を README に書けば十分。

### TD-33 `tui_test.go`（2,800行）の分割 — ✅ done (PR #41)
テーブル/ヘルパー/キー操作/リフレッシュ等でファイル分割したいが、テスト資産としては機能している。TD-21/23 のついでに移動する。

### TD-34 `wellKnownServices` マップ（80行）が tui_helpers.go に直書き — ✅ done (PR #41)
[tui_helpers.go:58–96](../internal/ui/tui_helpers.go)。ポート→サービス名の静的データ。動作に問題なし。UI 分割時に `services.go` 等へ移すだけでよい。

### TD-35 UDP ポートチェックが空ペイロード送信
[portchecker.go:174](../internal/pinger/portchecker.go) の `conn.Write([]byte{})`。UDP スキャンとして精度が低い（Open|Filtered が大半になる）が、これは UDP の性質であり表示上も明示されている。プロトコル別プローブ（DNS クエリ等）は機能追加であってリファクタリングではない。

### TD-45 4つのモニタ描画関数（traceroute/port/MTR/HTTP）の構造重複 — ✅ done (PR #42)
- **概要（architecture_review.md §8-3 由来、当初 technical_debt_inventory.md に未転記だった項目）**: `renderTracerouteTable`（render_monitors.go）・`renderPortMonitorTable`（render_monitors.go）・`renderMTRTable`/`renderMTRTargetTable`（mtr_view.go）・`renderHTTPMonitorTable`（http_view.go）がそれぞれ独自に「compact判定→列幅計算→ヘッダ/罫線→行ループ」を実装している。罫線描画自体は `boxtable.go`（`boxBorder`/`boxHeaderRow`/`boxSpanRow`）で共通化済みだが、それ以外は4関数に分散していた。
- **調査結果**: 列幅計算・compact判定・行ループはテーブルごとにデータモデルが本質的に異なる（tracerouteは複数行ラップ、MTRはターゲット毎の副テーブル+ラベル行、port/HTTPは行単位）ため、TD-21のような単一の `column` 構造体への統合は複雑さに見合わない（YAGNI）と判断。一方 `renderPortMonitorTable` と `renderHTTPMonitorTable` の「ステータス変化検知→ログ追記」ブロックはロジックが完全に同型で、レイアウトと無関係な純粋ロジックだったため安全に切り出し可能と判断し、共通ヘルパーに統合した。
- **対応**: ステータス変化ログ検知ロジックのみ共通ヘルパー化。列幅計算・compact判定・行ループの構造重複は意図的に残置（各テーブル固有のレイアウト要件があるため、無理な統合はしない）。
- **優先度: 低 / リスクレベル: 低**

---

## 分類5: 現時点では触らないほうがよい負債

### TD-40 pinger のワーカー/レシーバ並行モデル
[pinger.go:207–433](../internal/pinger/pinger.go)。ICMP ID ベースのチャネル分配、1秒ポーリングの受信ループ、done チャネルの select ガードなど、素朴だが実績のある構造。「レシーバを context 化したい」「ポーリングを排したい」という誘惑はあるが、macOS raw socket の癖（traceroute.go:81–84 のコメント参照）を吸収している現物であり、**リグレッションリスクに対してリターンがない**。CI に -race が入り（TD-02）、実機テスト手順が整うまで凍結。

### TD-41 PMTU 探索のプラットフォーム分岐と DF 制御
[pmtu.go](../internal/pinger/pmtu.go) + pmtu_darwin/linux/other。macOS の `IP_DONTFRAG`、受信バッファサイズの理由（pmtu.go:166–170 のコメント）など、デバッグで獲得した知見の塊。CI が darwin をカバーしていない現状では触らない（TD-02 で macOS ジョブを足すのが先）。

### TD-42 `extractEchoIDSeq` の多段フォールバックパース
[icmp_errors.go](../internal/pinger/icmp_errors.go)。ICMP エラーボディ→内包 IP ヘッダ→UDP ヘッダ→シグネチャスキャンの防御的パースは、プラットフォーム/ルーター実装差の吸収層。冗長に見えても削らない。

### TD-43 mtr.Engine の torn-round 破棄ロジック
[engine.go:208–218](../internal/mtr/engine.go)。直近コミット `9ee0dce` で入ったばかりのレース修正。安定観察期間を置く。

### TD-44 tview の描画ハック（makeDoubleBorderDrawFunc、黒背景の明示クリア）
[tui_helpers.go:692](../internal/ui/tui_helpers.go)、[graph.go:344–349](../internal/ui/graph.go)。tview/tcell の描画残留への対処で、環境依存の見た目問題を背負っている。リファクタで「きれいに」しようとすると特定端末での表示崩れが再発しやすい。

---

## 優先度マトリクス（サマリ）

| ID | 負債 | 優先度 | リスク | 分類 |
|---|---|---|---|---|
| TD-01 | コミット済みバイナリ `main` | 高 | 中 | 1 |
| TD-02 | CI に -race/vet/lint なし | 高 | 高 | 1 |
| TD-03 | updateTable の DNS 列無視 | 高 | 低 | 1 |
| TD-04 | HTTP ログ追記のエビクション不整合 | 高 | 中 | 1 |
| TD-05〜TD-12 | 各種二重実装・小掃除 | 中 | 低 | 2 |
| TD-19 | 設定配管5点セット | 中 | 中 | 3 |
| TD-20 | traceroute/MTR 統合 | 中 | 高 | 3 |
| TD-21 | UI 列モデル | 中 | 中 | 3 |
| TD-22 | run() 分解 | 中 | 高 | 3 |
| TD-23 | ui.Run() 分解 | 中 | 中 | 3 |
| TD-24 | "host (ip)" 型化 | 低〜中 | 中 | 3 |
| TD-25 | port ホットリロード無視 | 中 | 中 | 3 |
| TD-26 | watcher 死後の無警告 | 低 | 中 | 3 |
| TD-30〜35 | 低優先 | 低 | 低 | 4 |
| TD-40〜44 | 凍結（触らない） | — | — | 5 |
