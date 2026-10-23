# gh-sim

[![CI](https://github.com/a2-ito/gh-sim/actions/workflows/ci.yml/badge.svg)](https://github.com/a2-ito/gh-sim/actions/workflows/ci.yml)

GitHub Actions のワークフローを **実行せずに** ドライランするコマンドです。

イベントを与えると、どのワークフローが起動し、どのジョブとステップが実行されるかを判定して表示します。Docker もネットワークも使いません。

```console
$ gh-sim run -event push -ref main -steps
CI  .github/workflows/ci.yml
  ✔ 起動する — on.push に一致します
  ├─ ✔ Lint  ubuntu-latest · 4 steps
  ├─ ✔ test  needs: lint · matrix: node × os → 5 件
  │  ├─ ✔ Test (node 18 / ubuntu-latest)  ubuntu-latest · 5 steps
  │  │  ├─ ✔ Run actions/checkout@v4
  │  │  ├─ ✔ Run npm test
  │  │  └─ ✘ Upload coverage
  │  │     if が偽: ${{ matrix.coverage }}
  │  └─ ✔ Test (node 22 / ubuntu-latest)  ubuntu-latest · 5 steps
  └─ ✘ deploy  ubuntu-latest · 2 steps · needs: lint, test
     if が偽: github.ref == 'refs/heads/main' && github.event_name == 'push'
ワークフロー 3 件中 1 件が起動 · ジョブ 実行 6 件 · スキップ 1 件
```

## `act` との違い

[`act`](https://github.com/nektos/act) はジョブを実際に実行します。`gh-sim` は実行しません。

`gh-sim` が答えるのは「**このイベントで何が動くのか**」という問いです。

- このブランチへの push で、そのワークフローは本当に起動するのか
- `if:` を書き換えたら、どのジョブがスキップされるようになるのか
- `matrix` は結果的に何ジョブに展開されるのか。`include` / `exclude` は意図どおりか
- `needs` の依存関係はどうつながっているか。1つスキップされたら何が連鎖するか

これらを Docker なし・数十ミリ秒で確認できます。

## インストール

```console
go install github.com/a2-ito/gh-sim/cmd/gh-sim@latest
```

または各プラットフォーム向けのバイナリをビルドします。

```console
make dist
```

## 使い方

### `run` — ドライラン

```console
# main への push
gh-sim run -event push -ref main

# pull request（branches フィルタはマージ先と照合されます）
gh-sim run -event pull_request -action opened -base-ref main -ref feature/x

# タグへの push
gh-sim run -event push -ref refs/tags/v1.2.3

# 変更ファイルを与えて paths / paths-ignore を評価させる
gh-sim run -changed src/main.go,README.md

# 手動実行の入力を与える
gh-sim run -event workflow_dispatch -input dry-run=false -input target=prod

# 前段ジョブの出力を与えて、それに依存する判定を確定させる
gh-sim run -output changes.environment=develop

# ステップまで表示し、起動しないワークフローも理由付きで出す
gh-sim run -steps -all

# CI で使う場合は JSON で
gh-sim run -json
```

主なオプション:

| オプション                             | 説明                                                              |
| -------------------------------------- | ----------------------------------------------------------------- |
| `-dir`                                 | ワークフローディレクトリ（既定: `.github/workflows`）             |
| `-file`                                | 単一ファイルだけを対象にする                                      |
| `-event`                               | イベント名（既定: `push`）                                        |
| `-action`                              | アクティビティタイプ（`opened`、`closed` など）                   |
| `-ref`                                 | 対象の ref。`refs/` を省くとブランチ名として扱われます            |
| `-base-ref`                            | pull request のマージ先ブランチ                                   |
| `-changed`                             | 変更されたファイル（カンマ区切り、繰り返し可）                    |
| `-payload`                             | イベントペイロードの JSON ファイル                                |
| `-input` / `-env` / `-var` / `-secret` | `key=value` 形式で繰り返し指定                                    |
| `-output`                              | ジョブの出力（`ジョブID.出力名=value`）。与えると判定が確定します |
| `-steps`                               | ステップの一覧も表示                                              |
| `-all`                                 | 起動しないワークフローも表示                                      |
| `-notes`                               | 判定を保留した項目の注記（既定: 有効）                            |
| `-json`                                | JSON で出力                                                       |

### `eval` — 式を1つ評価する

`${{ }}` は省略できます。

```console
$ gh-sim eval "github.ref_name == 'main'" -ref main
true	(boolean, 条件としては 真)

$ gh-sim eval "github.ref_name == 'main' && 'prod' || 'dev'" -ref develop
dev	(string, 条件としては 真)

$ gh-sim eval "contains(fromJSON('[1,2,3]'), '2')"
true	(boolean, 条件としては 真)
```

### `parse` — 構造の確認

```console
gh-sim parse
```

## 再現している仕様

式言語（`${{ }}`）の癖の多い部分を含め、次を再現しています。

| 項目                          | 内容                                                                                                    |
| ----------------------------- | ------------------------------------------------------------------------------------------------------- |
| 比較の型変換                  | `'1' == 1` は真。型が違えば数値に変換して比較                                                           |
| 文字列比較                    | `'a' == 'A'` は真（大文字小文字を区別しない）                                                           |
| `&&` / `                      |                                                                                                         | `   | 真偽値ではなく被演算子の値を返す（`cond && 'a' |     | 'b'` が三項演算子として働く） |
| 演算子の優先順位              | `!` は比較より強いので `!a == false` は `(!a) == false`                                                 |
| オブジェクトフィルタ          | `github.event.commits.*.message`                                                                        |
| 組み込み関数                  | `contains` / `startsWith` / `endsWith` / `format` / `join` / `toJSON` / `fromJSON` / `hashFiles`        |
| ステータス関数                | `success()` / `failure()` / `cancelled()` / `always()`                                                  |
| トリガー照合                  | `branches` / `tags` / `paths` とその `-ignore`、`types`、`!` による除外                                 |
| フィルタパターン              | `*` はスラッシュを跨がない。`?` と `+` は直前の要素への量指定子（glob とは異なる）                      |
| `branches` のみ指定           | タグへの push では起動しない（逆も同様）                                                                |
| `pull_request` の `branches`  | head ではなくマージ先（base）と照合                                                                     |
| `pull_request` の既定 `types` | `opened` / `synchronize` / `reopened` のみ                                                              |
| `matrix` の展開               | 直積 → `exclude` → `include` の順。`include` は元の軸を上書きせず、該当が無ければ新しい組み合わせを作る |
| `needs`                       | トポロジカル順の評価、レイヤ算出、循環検出                                                              |
| 暗黙の `if`                   | `if` を省略すると `success()` が適用され、依存がスキップされると後続も連鎖してスキップ                  |
| 不明値の伝播                  | 出力に依存する条件は `?`（判定不能）になり、判定不能なジョブに依存する後続も `?` に連鎖する             |
| `hashFiles` のパターン        | `paths` フィルタとは別構文（`**/x` はルートの `x` にも一致、`?` は任意1文字）                           |

## 再現していないこと

ドライランなので、次は判定できません。**分からないものは黙って埋めず、注記として表示します**（`-notes`）。

- **ジョブやステップの実際の実行**。`run:` の中身は解析しません
- **`secrets` の値**。指定しない限り `null` として評価されるため、`secrets.X != ''` のような条件は実際と異なります（`-secret` で与えられます）
- **`steps.<id>.outputs` と `needs.<id>.outputs` の値**。`run:` の中で決まるため静的には分かりません。
  これらを参照した条件は**偽と断定せず `?`（判定不能）として表示**します。真になる場合も実際の値次第で覆るため同じ扱いです。
  `-output ジョブID.出力名=value` で値を与えれば確定判定になります。
  `needs.<id>.result` はスキップ判定を反映するので確定値として扱われます
- **再利用可能ワークフローや composite action の中身**。呼び出しとしてのみ表示します
- **実行時の成否**。既定ではすべて成功したものとして扱うため、`failure()` は偽になります
- **`schedule` の時刻**。cron 式の次回実行時刻は計算しません
- **`concurrency` / `timeout-minutes` / ランナーの空き状況** による影響
- **イベントペイロードの実際の内容**。`-payload` を渡さない場合は最小限の内容を合成します

## サンプルで試す

`examples/` に実運用でよく現れる書き方のサンプルと、試すイベント条件の一覧が入っています。

```console
make demo                      # 全シナリオを実行して結果を表示
make demo-one NAME=push-tag    # 1つだけ実行
make scenarios                 # シナリオ名の一覧
```

各シナリオの期待出力は `examples/golden/` に固定してあり、`make e2e`（`make test` にも含まれる）で検証します。判定ロジックを変えたときに、どのシナリオの結果が変わったかが差分で分かります。

```console
make e2e          # golden と一致するか検証
make e2e-update   # golden を現在の出力で更新（git diff で差分を確認する）
```

詳細は [examples/README.md](examples/README.md) を参照してください。

## 開発

```console
make help     # ターゲット一覧
make test     # テスト（サンプルの検証を含む）
make cover    # カバレッジ
make all      # vet + test + build
make dist     # 各プラットフォーム向けバイナリ
```
