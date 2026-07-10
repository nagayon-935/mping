# mping 削除候補リソース一覧

- 調査日: 2026-07-09 / 対象コミット: `68215cd`
- 検出方法: `deadcode ./...`（golang.org/x/tools、main からの到達可能性解析）、識別子ごとの参照 grep、`git ls-files`（追跡状態）、ファイルタイムスタンプ確認
- 削除実施前の共通ゲート: **各削除は独立コミットとし、削除後に `go build ./... && go vet ./... && go test -race ./...` が通ること**
- 関連: [technical_debt_inventory.md](./technical_debt_inventory.md)（TD-xx 参照）、[refactoring_master_plan.md](./refactoring_master_plan.md)（実施フェーズ）

---

## カテゴリ1: 未使用のクラス（型）

**完全に未使用の型は検出されなかった。** 全公開型（`Pinger`, `TargetStats`, `MTRStats`, `PortCheckResult`, `HTTPCheckResult`, `GraphView`, 各 View 構造体等）は本番コードから到達可能。

ただし「本番コードでは実質死んでいる型」が1つある: `portSeries`（[graph.go:39–60](../internal/ui/graph.go)）。`GraphView.showPorts` が本番では常に `false` のため、テストからしか生成されない。これは型の問題ではなくフラグの問題なので **カテゴリ4（DEL-10）** で扱う。

---

## カテゴリ2: 未使用の関数

### DEL-01 `pinger.NewPinger`
- **概要**: `NewPingerWithOptions(targets, Options{})` の薄いラッパー。本番の生成経路は cmd/main の `newPinger` 変数 → `NewPingerWithOptions` のみ。
- **該当**: [pinger.go:111–114](../internal/pinger/pinger.go)
- **根拠**: `deadcode ./...` が `unreachable func: NewPinger` を報告。非テストコードでの呼び出しゼロ（grep `\bNewPinger\b` は定義行のみ）。テストでは多数使用。
- **確認方法**: `grep -rn --include='*.go' -w 'NewPinger' . | grep -v _test.go` が定義行のみであること。
- **リスク**: 低。`internal/` 配下のため外部モジュールからの import は言語仕様上不可能（隠れた利用者は存在しえない）。テストのコンストラクタとして便利なので、**削除せずテスト専用と割り切って残す選択も妥当**（その場合はコメントで明示）。
- **LLM 依頼単位**: DEL-02〜05 と合わせて1プロンプト（下記「一括削除プロンプト」参照）。

### DEL-02 `pinger.PortSpec.String()`
- **概要**: `"443/tcp"` 形式のフォーマッタ。本番コードは表示箇所で `fmt.Sprintf("%d/%s", pr.Port, pr.Protocol)` を直接使っており（render_monitors.go:137, 213 等）、この String() を通らない。
- **該当**: [portchecker.go:21–24](../internal/pinger/portchecker.go)
- **根拠**: `deadcode` が unreachable と報告。非テスト参照ゼロ（portchecker_test.go:80 のみ）。
- **確認方法**: `PortSpec` 値が `%v`/`%s` でフォーマットされる箇所がないこと（fmt.Stringer 経由の動的呼び出し）: `grep -rn 'Sprintf.*spec\|Printf.*spec\|%v.*PortSpec' --include='*.go' .`
- **リスク**: 低〜中。fmt.Stringer は暗黙インターフェースのため grep で見えにくい（上記確認必須）。逆に「表示側が String() を使うべき」という統一（削除ではなく採用）も合理的 — **推奨は採用側**（重複フォーマット3箇所を String() 呼び出しに置換）。
- **LLM 依頼単位**: 1プロンプト(削除 or 採用の判断込み)。

### DEL-03 `stats.TargetStats.SetASN`
- **概要**: ASN 番号のみを設定する旧セッター。`SetASNInfo(number, country, org)`（stats.go:296）の導入で置き換えられた。
- **該当**: [stats.go:290–294](../internal/stats/stats.go)
- **根拠**: 非テスト参照ゼロ（stats_test.go:249 のみ）。本番の呼び出しは `lookupASN` → `SetASNInfo` のみ。
- **確認方法**: `grep -rn --include='*.go' -w 'SetASN' . | grep -v _test.go` が定義行のみ。
- **リスク**: 低。テスト1箇所を `SetASNInfo("AS12345", "", "")` に書き換えるだけ。
- **LLM 依頼単位**: 一括削除プロンプトに含める。

### DEL-04 `stats.PortCheckResult.GetResult`
- **概要**: 5値タプルを返す旧スナップショット API。`GetView()`（stats.go:84、RTT統計・履歴込み）の導入で置き換えられた。
- **該当**: [stats.go:77–81](../internal/stats/stats.go)
- **根拠**: 非テスト参照ゼロ（portchecker_test.go ×3、stats_test.go ×4 のみ）。本番の読み取りは全て `GetView()` 経由。
- **確認方法**: `grep -rn --include='*.go' -w 'GetResult' . | grep -v _test.go` が定義行のみ。
- **リスク**: 低。ただしテスト7箇所の書き換えが必要（`GetView()` のフィールド参照へ機械的に変換）。
- **LLM 依頼単位**: 一括削除プロンプトに含める。

### DEL-05 `pinger.GetASNFor`
- **概要**: ASN 番号文字列のみ返す公開ラッパー。MTR アダプタは `GetASNInfoFor`（mtr_probe.go:91）を使っており、こちらは使われていない。
- **該当**: [mtr_probe.go:82–88](../internal/pinger/mtr_probe.go)
- **根拠**: 非テスト参照ゼロ（coverage_extra_test.go:99, 119 のみ）。
- **確認方法**: `grep -rn --include='*.go' -w 'GetASNFor' . | grep -v _test.go` が定義行のみ。
- **リスク**: 低。テスト2箇所を `GetASNInfoFor(...).Number` に書き換え。
- **LLM 依頼単位**: 一括削除プロンプトに含める。

> **一括削除プロンプト例（DEL-01〜05）**: 「internal/pinger/pinger.go の NewPinger、internal/pinger/portchecker.go の PortSpec.String、internal/stats/stats.go の SetASN と GetResult、internal/pinger/mtr_probe.go の GetASNFor は非テストコードから未参照である。各関数について (a) fmt.Stringer 等の動的参照がないことを grep で確認し、(b) 削除して参照テストを現行 API（NewPingerWithOptions / GetView / SetASNInfo / GetASNInfoFor）へ書き換え、(c) `go test -race ./...` と `deadcode ./...` の結果を提示せよ。判断が分かれるもの（NewPinger のテスト用途残置、PortSpec.String の採用統一）は削除せず提案として報告せよ。」

---

## カテゴリ3: 未使用の定数

**検出なし。** 主要 const ブロック（mping.go:25–33、pinger.go:21–30、tui.go:16–20、tui_helpers.go:16–20、graph.go:13–20、mtr_view.go:11–17、http_view.go:15–22、stats.go:8–11、engine.go:13–18、watcher.go:13）の全定数について参照 grep を実施し、いずれも使用中。

- **確認方法（再現手順）**: 各 const 名について `grep -rn --include='*.go' -w '<NAME>' .` の参照が定義行以外に1件以上あること。`staticcheck`（U1000）導入後（TD-02）は未使用非公開定数が CI で自動検出される。

---

## カテゴリ4: 古い Feature Flag（機能フラグ）

### DEL-10 `GraphView.showPorts`（本番で常に false のフラグ）
- **概要**: RTT グラフにポートチェックの系列も描く機能フラグ。本番の唯一の生成箇所 [tui.go:111](../internal/ui/tui.go) が `NewGraphView(targets, interval, false)` と定数 false を渡しており、有効化する経路（CLI フラグ・YAML キー）が存在しない。true で動くのはテストのみ。
- **該当**: [graph.go:66,77,91–103](../internal/ui/graph.go)（`showPorts` フィールド、コンストラクタ引数、`buildSeries` の分岐）、[graph.go:39–60](../internal/ui/graph.go)（`portSeries` 型）
- **根拠**: `grep -rn 'NewGraphView' --include='*.go' . | grep -v _test.go` → tui.go:111 の1箇所のみ、第3引数リテラル false。
- **削除前に確認すべきこと**: これが「未完の新機能」か「撤退した機能」かはコードからは判別できない。`git log --oneline -S 'showPorts'` で導入コミットの意図を確認し、**所有者判断を仰ぐ**こと。ポートグラフを製品化する予定があるなら削除ではなく「-p 指定時に true を渡す」1行の変更で有効化できる。
- **リスク**: 中（機能意図の誤読）。削除する場合は graph.go の約40行 + テスト削減で、描画コアは無傷。
- **LLM 依頼単位**: 1プロンプト。「showPorts を (A)削除 (B)-p 指定時に有効化 の両案を diff 付きで提示せよ。決定は人間が行う」という比較依頼が安全。

---

## カテゴリ5: 参照されていないリソース（アセット等）

### DEL-20 `.DS_Store`（4ファイル、macOS Finder の残骸）
- **該当**: `./.DS_Store`, `.github/.DS_Store`, `cmd/.DS_Store`, `internal/.DS_Store`
- **根拠**: macOS Finder が自動生成するメタデータ。git 未追跡（.gitignore 済み）。
- **確認方法**: `git ls-files | grep DS_Store` が空（= 追跡されていない）を確認済み。
- **リスク**: なし。`find . -name '.DS_Store' -delete` で即削除可。
- **LLM 依頼単位**: DEL-21/22 と合わせて1プロンプト（ローカル掃除一括）。

### DEL-21 古いカバレッジ成果物（5ファイル、最古は2026-03）
- **該当**: `cover.out`（4/7）、`coverage.out`（6/26）、`coverage.txt`（3/24）、`profile.cov`（4/17）、`ui_coverage.out`（3/22）
- **根拠**: いずれも git 未追跡・.gitignore 対象（`*.out` / `coverage.*` / `profile.cov`）。`make coverage` 等の過去実行の残骸で、出力名がバラバラなこと自体が「その場しのぎ実行」の証跡。
- **確認方法**: `git ls-files | grep -E '\.(out|cov)$|coverage'` が空であること（確認済み）。
- **リスク**: なし（再生成可能）。今後は `make coverage` の出力名（coverage.out）に統一する運用にすると再発しない。
- **LLM 依頼単位**: DEL-20/22 と同一プロンプト。

### DEL-22 ローカルビルドバイナリ `mping`（11.8MB、未追跡）と `main`（11.8MB、**追跡中**）
- **該当**: `./mping`（未追跡・ignore 済み）、`./main`（**git 追跡中** — [TD-01](./technical_debt_inventory.md) 参照）
- **根拠**: `git ls-files` に `main` が含まれる（コミット `2ae5535` で混入）。`.gitignore` は `mping` のみ記載で `main` が漏れている。
- **確認方法**: `git log --oneline -- main` で混入コミット特定済み。`file main` でバイナリ形式確認可。
- **リスク**: `main` の削除はなし（コード参照ゼロ）。**注意**: `git rm main`（--cached でなく）はローカルファイルも消すため、`git rm --cached main && echo main >> .gitignore` の手順とする。
- **LLM 依頼単位**: 1プロンプト（TD-01 と同一。gitignore 追記・追跡解除・確認コマンド提示）。

---

## カテゴリ6: 使われていない設定ファイル

### DEL-30 `test-groups.yaml`（リポジトリ直下にコミットされた手動テスト用設定）
- **概要**: グループ表示機能の手動確認用サンプル（Cloudflare/Google DNS/Japan の3グループ + 公開 DNS/ISP の実在 IP）。
- **該当**: `./test-groups.yaml`（git 追跡中）
- **根拠**: コード・テスト・CI・README のいずれからも参照されていない（`grep -rn 'test-groups' . --include='*.go' --include='*.yml' --include='*.md'` → 0件。README は `hosts.yaml` という別名の例を掲載）。
- **削除前に確認すべきこと**: 開発者の手動テスト手順（`./mping -f test-groups.yaml`）として現役の可能性。**削除より `examples/hosts-groups.yaml` への移動 + README からのリンクが推奨**（グループ機能の実例ファイルとしてむしろ活用余地がある）。
- **リスク**: 低。
- **LLM 依頼単位**: 1プロンプト（examples/ へ移動し README 両言語版にリンク追記、または削除。どちらかを指定して依頼）。

### DEL-31 `.claude/settings.local.json` 内の陳腐化した許可エントリ
- **概要**: Claude Code のローカル許可リストに、旧プロジェクトパス `/Users/ryu/mping`（現在は `~/dev/projects/mping`）を固定で含む find/grep/sed/perl の1回限りコマンド許可が約20件蓄積している。
- **該当**: [.claude/settings.local.json](../.claude/settings.local.json)
- **根拠**: パス `/Users/ryu/mping` は現存しない。`awk 'NR>=1100 && NR<=1110' internal/ui/tui.go` のような行番号固定の許可は再利用不能。
- **削除前に確認すべきこと**: このファイルは git 未追跡（ユーザーローカル）。整理は動作に影響しないが、`Bash(go:*)` 等の汎用許可は残すこと。
- **リスク**: なし（消しすぎても再承認プロンプトが出るだけ）。
- **LLM 依頼単位**: 1プロンプト（「旧パス・行番号固定・1回限りの許可を除去し、汎用許可のみ残せ」）。

---

## カテゴリ7: 使われていないバッチ・スクリプト

**該当なし。** スクリプトは `install.sh` の1本のみで、①`Makefile` の `install` ターゲット、②リリースワークフロー（release.yml が tar.gz に同梱）、③README のインストール手順、の3箇所から現役参照されている。

- **確認方法**: `grep -rn 'install.sh' Makefile .github/ README*.md` で3系統の参照を確認済み。

---

## カテゴリ8: 過去施策（クローズした機能）の残骸

### DEL-40 `PMTUBottleneckIP` の write-only データ経路
- **概要**: PMTU 探索で検出したボトルネックルーターの IP を `TargetStats` に保存する経路が、**書き込まれるだけでどこからも読まれない**。UI のどのペインにも描画されず、JSON エクスポート（export.go の `TargetSummary`）にもフィールドがない。ユーザーへの情報提供は preLogs 経由のログ行（`[PMTU] mtu mismatch at: <ip>`、pmtu.go:83–85）だけで完結しており、構造体への保存経路は残骸化している。
- **該当**: [stats.go:116](../internal/stats/stats.go)（フィールド）、stats.go:186（View フィールド）、stats.go:252（GetView コピー）、stats.go:329–333（`SetPMTUBottleneckIP`）、[mping.go:783–785](../cmd/main/mping.go)（唯一の書き込み）
- **根拠**: `grep -rn 'PMTUBottleneckIP' --include='*.go' . | grep -v _test.go` の全ヒットが「定義・コピー・セット」のみで、読み出し（表示・エクスポート）が存在しない。
- **削除前に確認すべきこと**: 「JSON エクスポートに載せる予定だった」可能性。**選択肢は (A) フィールド一式を削除、(B) export.go の `TargetSummary` に `pmtu_bottleneck_ip` を追加して活かす**。JSON 出力の利用者がいるなら (B) が数行で価値になる。
- **リスク**: 低。(A) の場合 stats のフィールド削除 + mping.go 1箇所 + テスト調整。
- **LLM 依頼単位**: 1プロンプト（A/B 両案の diff 提示 → 人間が選択）。

### DEL-41 `graph.go` の実装変更の痕跡（`_ = gy100` / `_ = totalSteps`）
- **該当**: [graph.go:431–432](../internal/ui/graph.go)
- **根拠**: 戻り値を受けて即座に捨てており、グリッド描画は `gridSteps` 配列経由に移行済み。過去のリファクタの消し忘れ。
- **リスク**: なし。[TD-11](./technical_debt_inventory.md) として UI 掃除プロンプトに同梱。

---

## カテゴリ9: 削除判断に注意が必要なもの（動的参照の可能性等）

### DEL-50 `PortSpec.String()` の fmt.Stringer 経由の暗黙呼び出し（DEL-02 の注意点）
`%v` / `%s` フォーマットは grep に映らない動的ディスパッチで String() を呼ぶ。deadcode（RTA 解析）が unreachable と判定しているため現状は安全だが、**削除 PR では「PortSpec を fmt 系関数に渡している箇所がない」ことの grep 証跡を必須とする**。

### DEL-51 `showPorts`（DEL-10）の「未完機能 vs 撤退機能」判定
テストが showPorts=true の挙動を丁寧に固定していることから、実装者に将来の有効化意図があった可能性が高い。削除は所有者確認後に。

### DEL-52 `test-groups.yaml`（DEL-30）の手動テスト用途
グループ機能開発時の検証ファイルであり、開発者のローカル手順（シェル履歴等）から参照されている可能性がある。削除ではなく examples/ 移動を推奨。

### DEL-53 テスト専用 API の削除に伴うカバレッジ低下
DEL-01〜05 はテストからは参照されているため、削除するとそのテストも消え、**見かけのカバレッジが数%変動する**（coveralls バッジに影響）。削除 PR には「カバレッジ低下は dead API のテスト削除によるもの」と明記すること。

### DEL-54 `main` バイナリの git 履歴からの完全除去（filter-repo）は実施しない
DEL-22/TD-01 の追跡解除で今後の肥大は止まるが、履歴には 11.8MB が残る。`git filter-repo` による履歴書き換えは**公開リポジトリの全 clone/fork の SHA を無効化する破壊的操作**のため、明示的な所有者判断なしに実施しないこと。

---

## 実施サマリ（推奨順）

| 順 | ID | 内容 | 種別 | 事前確認 |
|---|---|---|---|---|
| 1 | DEL-20/21/22 | .DS_Store・カバレッジ残骸・バイナリ掃除 + `main` 追跡解除 | ローカル+git | git ls-files |
| 2 | DEL-31 | settings.local.json の陳腐化許可の整理 | ローカル設定 | なし |
| 3 | DEL-03/04/05 | SetASN / GetResult / GetASNFor 削除（テスト書き換え込み） | コード | grep 証跡 |
| 4 | DEL-01/02 | NewPinger / PortSpec.String の削除 or 用途明確化 | コード | Stringer 確認 |
| 5 | DEL-30 | test-groups.yaml → examples/ 移動 + README リンク | リソース | 所有者確認 |
| 6 | DEL-40 | PMTUBottleneckIP の削除 or JSON 露出 | コード | 所有者判断 |
| 7 | DEL-10 | showPorts の削除 or 有効化 | コード | 所有者判断 |
