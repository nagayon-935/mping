# mping リファクタリングマスタープラン

- 策定日: 2026-07-09 / 対象コミット: `68215cd`（main）
- 本計画は3つの詳細ドキュメントの上位計画である:
  - [architecture_review.md](./architecture_review.md) — 構造・依存・設計パターンの現状評価（調査観点「プロジェクト構造 / 依存関係 / 共通処理 / 肥大化」の詳細）
  - [technical_debt_inventory.md](./technical_debt_inventory.md) — 負債の全量（TD-01〜TD-44、5分類）
  - [deletion_candidates.md](./deletion_candidates.md) — 削除候補の全量（DEL-01〜DEL-54、9分類）
- 実測ベースライン: `go vet` クリーン / 全テスト成功 / カバレッジ cmd/main 82.7%・mtr 83.3%・pinger 79.8%・stats 92.9%・ui 89.8%・watcher 77.1% / 実装 約8.7k行・テスト 約11k行

---

## 1. リファクタリングの目的

1. **変更コストの支配要因を除去する。** 現在、(a) 設定項目の追加に5箇所の同期修正、(b) 新モニタ種別の追加に6層の手書き配線、(c) テーブル列の変更に並行スライス+マジックインデックスの同期、が必要（根拠: architecture_review §9–10）。これらを「1〜2箇所の変更で済む」構造にする。
2. **並行処理の安全性を検証可能にする。** shutdown 順序・ホットリロードの正しさが現状コメントと手動確認頼み。race detector 付き CI と特性テストで機械検証に置き換える。
3. **無駄の除去。** コミット済みバイナリ、テスト専用 API、write-only データ経路、死んだ機能フラグ（deletion_candidates 全量）を計画的に削除する。
4. **やらないことを決める。** pinger の並行モデル・PMTU のプラットフォーム分岐・防御的パース（TD-40〜44）は実績あるコードとして凍結し、リファクタ対象から明示的に除外する。

**非目的**: 機能追加、UI デザイン変更、パフォーマンスチューニング（実測上の問題がない）、アーキテクチャの全面刷新（層構造は健全であり維持する）。

## 2. 現状の問題点（要約）

| # | 問題 | 詳細参照 |
|---|---|---|
| P1 | 2つの神関数: `run()` 590行（mping.go:810–1402）と `ui.Run()` 800行（tui.go:74–871）にライフサイクル・描画制御が集中 | TD-22, TD-23 |
| P2 | 構造的重複: traceroute/MTR プローブ二重実装、RTT記録三重実装、ジッタ計算・閾値適用・エラーログ追記・エラー文字列正規化の各二重実装 | TD-05〜08, 10, 20 |
| P3 | 暗黙結合: `"host (ip)"` 文字列プロトコル（7箇所）、UI 列のマジックインデックス（DNS/ASN 列で位置がずれる） | TD-03, 21, 24 |
| P4 | 設定配管の5点セット同期（フラグ/config/YAML/apply/validate） | TD-19 |
| P5 | 安全網の欠落: CI に -race / vet / lint / macOS ジョブがない | TD-02 |
| P6 | 無駄なリソース: 追跡済みバイナリ `main`(11.8MB)、テスト専用 API 5件、write-only フィールド、死にフラグ、ローカル残骸10件超 | DEL-01〜54, TD-01 |
| P7 | 静かな劣化: HTTP ログの無限伸長、port 設定のリロード無視、watcher 死後の無警告 | TD-04, 25, 26 |
| P8 | テストの偏り: カバレッジ自体は高いが、「停止順序」「リロードシーケンス」「列レイアウトの組み合わせ」という**リファクタで最も壊しやすい性質**を固定するテストがない | §5 |

## 3. フェーズ分割

依存関係: P0 → P1 → P2 は直列（安全網→掃除→小統合）。P3/P4/P5 は P2 完了後に**相互独立**（並行実施可）。P6 は任意。

### Phase 0: 安全網とリポジトリ衛生（コード挙動の変更なし）
- **対象範囲**: `.github/workflows/ci.yml`, `.gitignore`, リポジトリ直下の残骸, `CLAUDE.md`
- **実施内容**:
  1. TD-01/DEL-22: `main` の追跡解除 + .gitignore 追記
  2. DEL-20/21: .DS_Store・カバレッジ残骸のローカル掃除
  3. TD-02: CI 強化 — `gofmt -l` / `go vet` / `go test -race` / staticcheck（or golangci-lint）/ govulncheck / macOS ジョブ追加
  4. TD-12: CLAUDE.md の構造記述修正（`internal/pinger/` 配下への訂正）+ docs/ 4文書への参照追記
  5. DEL-30: `test-groups.yaml` → `examples/` 移動 + README リンク（所有者確認後）
- **完了条件**: 新 CI が main で全緑。`git ls-files` にバイナリなし。
- **このフェーズの意義**: 以降の全フェーズの回帰検出装置。**これが終わるまで一切のコードリファクタに着手しない。**

### Phase 1: デッドコード削除と即修正バグ
- **対象範囲**: internal/stats, internal/pinger, internal/ui の未使用 API・小バグ
- **実施内容**:
  1. DEL-03/04/05: `SetASN` / `GetResult` / `GetASNFor` 削除（テスト書き換え込み）
  2. DEL-01/02: `NewPinger` / `PortSpec.String` の削除 or 用途明確化（Stringer 動的参照の grep 証跡必須）
  3. DEL-40: `PMTUBottleneckIP` の削除 or JSON 露出（A/B 判断は人間）
  4. DEL-10: `showPorts` フラグの削除 or 有効化（A/B 判断は人間）
  5. TD-03: `updateTable` の DNS 列無視バグ修正 + dns×asn 4通りテスト
  6. TD-04: `appendErrorLogRaw` 廃止（HTTP ログ無限伸長の解消）
- **完了条件**: `deadcode ./...` の報告ゼロ（意図的残置はコメントで明示）。カバレッジがベースライン−2%以内。

### Phase 2: 低リスクの重複統合（パッケージ内で閉じる変更）
- **対象範囲**: internal/stats、internal/pinger の一部、internal/ui の小掃除
- **実施内容**:
  1. TD-06: ジッタ計算ヘルパー統合（stats 内）
  2. TD-07: `rttAccumulator` 導入で RTT 記録三重実装を統合（View/JSON 出力は不変）
  3. TD-08: trace ID 生成を `NextTraceID()` に一本化
  4. TD-05: `"write ip 0.0.0.0->"` 正規化を pinger 層に一本化
  5. TD-09/11: `updateTable` の二重 GetView 解消、graph.go の痕跡コード除去
- **完了条件**: 既存テストが（期待値変更を除き）無修正で通る。export_test.go は完全無修正。

### Phase 3: pinger 統合（高リスク・独立）
- **対象範囲**: internal/pinger/traceroute.go + mtr_probe.go
- **実施内容**: TD-20 — ①受理判定の特性テスト整備 → ②acceptPacket/acceptHopPacket 統合 → ③`TraceRoute` を `ProbeHop` ベースに書き換え（3段階、各段階で実機確認）
- **完了条件**: traceroute.go ≤100行、受信ロジック1系統。macOS/Linux 実機で `-T`/`-M` の出力が改修前と一致。

### Phase 4: cmd/main 分解（高リスク・独立）
- **対象範囲**: cmd/main/mping.go
- **実施内容**:
  1. TD-22: 停止順序・リロードの特性テスト → `supervisor` 抽出 → `reloadCoordinator` 抽出 → `run()` を150行以下に
  2. TD-19: 設定メタテスト導入 → `applyDocToCfg` の宣言化（③→②の段階導入、technical_debt_inventory 参照）
  3. TD-10: 閾値表現の `ui.Thresholds` への統一
  4. TD-25: port 設定リロード無視の警告表示（完全対応は supervisor 化後に判断）
  5. （任意）ネットワーク検出ヘルパーの `internal/netutil` 切り出し、ファイル分割（config.go / lifecycle.go / netdetect.go）
- **完了条件**: `go test -race ./cmd/...` 全緑 + 停止順序テスト・リロードシーケンステストが存在。実機で q/s/S/R キー・YAML リロード・count モードの手動スモーク成功。

### Phase 5: UI 再構築（中リスク・独立）
- **対象範囲**: internal/ui
- **実施内容**:
  1. TD-21: 描画ゴールデンテスト整備 → `column` 構造体導入 → 並行スライス・マジックインデックス撤廃
  2. TD-23: `monitorPane` データ駆動化 → キーハンドラ分離 → `Run()` を200行以下に
  3. TD-33/34: tui_test.go の機能別分割、`wellKnownServices` の `services.go` 移動
- **完了条件**: dns×asn×(groups有無)×(compact有無) の組み合わせで描画ゴールデン一致。列追加がデモ可能（試しに1列足して1箇所変更で済むことを確認し、revert）。

### Phase 6（任意）: 型の整理と運用改善
- **実施内容**: TD-24（`"host (ip)"` 文字列プロトコルの型化）、TD-26（watcher 再起動 or 明示警告）
- **判断基準**: Phase 4/5 完了後もこれらが実際に開発を妨げている場合のみ着手（YAGNI）。

## 4. スケジューリング方針

- **先に着手すべき**: Phase 0（安全網）が絶対の先頭。次に Phase 1（削除は差分が小さく、後続フェーズの対象コードを減らす）。Phase 2 は stats 中心で独立性が高く、成功体験と検証プロセスの習熟に適する。
- **後回しにすべき**: Phase 3/4 は高リスクのため、CI 安全網の実績（数週間の運用）ができてから。Phase 6 は「必要になるまでやらない」。
- **並行実施**: Phase 3（pinger）/ Phase 4（cmd/main）/ Phase 5（ui）はパッケージ境界で分離されており、ブランチを分けて並行可能。ただし**同時に進めるのは最大2本**とする（レビュー帯域と、cmd/main が pinger/ui 両方の呼び出し元である事実のため。Phase 4 と他フェーズの同時進行時は cmd/main 側の rebase 責務を Phase 4 担当に置く）。
- **凍結リスト（着手禁止）**: TD-40（pinger 並行モデル）、TD-41（PMTU プラットフォーム分岐）、TD-42（ICMP 防御パース）、TD-43（torn-round 破棄）、TD-44（tview 描画ハック）。これらに触れる提案が LLM から出た場合は却下する。

## 5. リスク管理

### リスクが高い箇所（変更時に必ず特別扱い）
| 箇所 | リスク | 対策 |
|---|---|---|
| `run()` の停止順序（trace join → mtr → pinger → port/http → watcher） | shutdown レース・goroutine リーク | 順序を特性テストで固定してから触る（Phase 4 先頭タスク）。`-race` + `goleak` 導入検討 |
| raw ソケット送受信（traceroute/MTR 統合） | モックで見えないタイミング差・OS 差 | 3段階分割 + 各段階で macOS/Linux 実機確認。`sudo ./mping -T 8.8.8.8` / `-M` の出力比較 |
| tview 描画（列モデル再編） | 端末依存の表示崩れ | ゴールデンテスト + iTerm2/Alacritty での目視スモーク |
| ICMP ID 空間（ping: `baseID+i` / trace: `baseID+0x1234+counter` / PMTU: `baseID`） | 統合時の ID 衝突 → 応答の誤配 | 割り当て規約を `NextTraceID` のコメントに集約（TD-08）し、統合 PR のレビュー観点に明記 |

### 事前に追加すべきテスト（各フェーズの先頭タスク）
1. **停止順序テスト**（Phase 4 前）: モック pinger/checker に停止記録を仕込み、stopAll の呼び出し順を検証
2. **リロードシーケンステスト**（Phase 4 前）: YAML 変更 → validate → TUI 停止 → 再構築の一連を fake watcher で検証（既存 mping_test.go の拡張）
3. **描画ゴールデンテスト**（Phase 5 前）: dns×asn×groups×compact の組み合わせで render 出力文字列を固定
4. **プローブ受理判定の特性テスト**（Phase 3 前）: EchoReply/TimeExceeded/DstUnreach × ID/Seq 一致・不一致のマトリクスを両実装（acceptPacket/acceptHopPacket）に適用し同一結果を確認
5. **設定メタテスト**（Phase 4）: `hostsFileYAML` の全フィールドが `applyDocToCfg` で処理されることを reflect で検査

### ロールバック方針
- **1 PR = 1 ID（TD-xx/DEL-xx）** を原則とし、すべての PR を独立 revert 可能にする。挙動保存コミットと挙動変更コミットを混ぜない。
- 各フェーズはフィーチャーブランチ（`refactor/phase-N-<topic>`）で進め、フェーズ完了時に main へマージ。フェーズ途中で回帰が出た場合はフェーズブランチごと破棄できる。
- リリースタグ運用: Phase 3/4 のマージ後は patch リリース（v0.4.x）を切って実環境フィードバック期間を置き、問題があれば直前タグへ即時ロールバック（バイナリ配布のため revert リリースが容易）。

## 6. 運用・レビュー方針

- **PR チェックリスト**(全 PR 共通):
  - [ ] `gofmt` / `go vet` / `go test -race ./...` / `deadcode ./...` の結果を PR 本文に貼付
  - [ ] カバレッジがベースライン（§冒頭）から 2% 以上低下していない（dead API 削除による低下は理由を明記）
  - [ ] 挙動変更の有無を宣言（「挙動保存」PR で期待値を変えたテストは、変更理由を1件ずつ説明）
  - [ ] pinger/pmtu を触る PR は macOS + Linux の実機スモーク結果（コマンドと出力）を添付
  - [ ] 対応する TD-xx/DEL-xx をタイトルに含める（例: `refactor: unify jitter calc (TD-06)`）
- **レビュー体制**: 実装 LLM とレビュー LLM を分離する（例: 実装 = GPT-5.5 / Opus 4.7、レビュー = go-reviewer 系エージェント）。凍結リスト（TD-40〜44）への変更が紛れ込んでいないかをレビューの必須観点とする。
- **ドキュメント運用**: 本 docs/ 4文書を living document とし、各 PR マージ時に該当 ID へ `✅ done (PR #nn)` を追記。四半期ごとに `deadcode` / カバレッジを再計測してベースラインを更新。
- **人間の判断が必要なゲート**（LLM に委任しない）: DEL-10（showPorts 削除 vs 有効化）、DEL-40（PMTUBottleneckIP 削除 vs JSON 露出）、DEL-30（test-groups.yaml の扱い）、DEL-54（git 履歴書き換えはしない）、Phase 6 着手判断。

## 7. LLM（GPT-5.5 / Opus 4.7 等）への依頼単位

### 切り出しの原則
1. **1プロンプト = 1 ID（TD-xx/DEL-xx）または明示されたサブステップ**。複数 ID の同梱は「同一ファイル内の小掃除」（TD-09+TD-11 等、各文書に同梱可と明記したもの）に限る。
2. **テスト追加と実装変更を別プロンプトに分ける**（高リスク項目 TD-20/21/22/23 は必須。特性テスト PR が先にマージされてから実装 PR）。
3. プロンプトには必ず以下を含める:
   - 対象ファイルの絶対パスと行範囲（本 docs/ の各項目に記載済み）
   - ゴール指標（例:「traceroute.go ≤100行」「GetView 呼び出しがターゲットあたり1回/tick」）
   - 検証コマンド（`go test -race ./...` 等）と、その結果の貼付要求
   - **スコープ外の変更禁止**の明示（特に凍結リスト TD-40〜44 と gofmt 以外の整形変更）
4. コンテキストとして渡すファイルは「対象ファイル + そのテスト + 本 docs/ の該当セクション」に限定する（リポジトリ全体を渡さない — 神関数 mping.go/tui.go は単体で 1,500/900 行あるため）。

### プロンプトテンプレート
```text
あなたは Go リファクタリングの実装者である。mping リポジトリ（github.com/nagayon-935/mping）で
以下のタスクを実施せよ。

## タスク: <TD-xx / DEL-xx のタイトル>
<docs/technical_debt_inventory.md または docs/deletion_candidates.md の該当項目を貼付>

## 制約
- 挙動保存リファクタである（期待値を変えるテスト修正には1件ずつ理由を付す）
- 対象ファイル以外の変更禁止。internal/pinger の並行モデル・pmtu_* には触れない
- コミットは <type>: <description> 形式、attribution なし

## 完了条件
- <ゴール指標>
- gofmt -l . が空 / go vet ./... クリーン / go test -race ./... 全緑
- 上記コマンドの実行結果を出力に含めること
```

### フェーズ → プロンプト対応表（全27プロンプト）

| フェーズ | プロンプト数 | 内訳 |
|---|---|---|
| Phase 0 | 4 | ①TD-01+DEL-20/21/22（衛生一括） ②TD-02a CI 強化 ③TD-02b lint 導入 ④TD-12+DEL-30（ドキュメント/サンプル整備） |
| Phase 1 | 5 | ①DEL-03/04/05 一括削除 ②DEL-01/02 判断込み削除 ③DEL-40 A/B 提案 ④DEL-10 A/B 提案 ⑤TD-03+TD-04 バグ修正 |
| Phase 2 | 4 | ①TD-06+TD-07（stats 統合） ②TD-08 ③TD-05 ④TD-09+TD-11 |
| Phase 3 | 3 | TD-20 の3段階（特性テスト → accept 統合 → TraceRoute 書き換え） |
| Phase 4 | 6 | TD-22 の4段階 + TD-19 の2段階（TD-10/25 は TD-22④・TD-19②に同梱） |
| Phase 5 | 6 | TD-21 の3段階 + TD-23 の3段階（TD-33/34 は最終段に同梱） |
| Phase 6 | 3（任意） | TD-24 の2段階 + TD-26 |

> 備考: DEL-31（settings.local.json 整理）はリポジトリ外のローカル設定のため、上表に含めず随時実施。
