# サンプルワークフロー

`gh-sim` の挙動を確認するためのサンプル一式です。実際の運用でよく現れる書き方を集めています。

```console
make demo                      # 全シナリオを実行して結果を表示
make demo-one NAME=push-main   # 1つだけ実行
make scenarios                 # シナリオ名の一覧
make e2e                       # 出力が golden と一致するか検証
make e2e-update                # golden を現在の出力で更新
```

## 構成

| パス | 内容 |
| --- | --- |
| `workflows/` | サンプルのワークフロー |
| `scenarios.txt` | 試すイベント条件の一覧（名前＋`gh-sim run` の引数） |
| `golden/` | 各シナリオの期待出力。`make e2e` が比較する |

## サンプルの意図

| ファイル | 確認できること |
| --- | --- |
| `ci-matrix.yml` | `matrix` の直積・`exclude`・`include`、組み合わせごとに変わるステップ条件、`runs-on` と `name:` への matrix 展開 |
| `deploy-gated.yml` | 前段の `outputs` に依存する条件が `?`（判定不能）になること。`-output` で確定させられること |
| `release-tags.yml` | `tags` フィルタ（ブランチへの push では起動しない）、`workflow_dispatch` の入力と既定値 |
| `monorepo-paths.yml` | `paths` フィルタと `!` による除外。`-changed` の有無で判定が変わる |
| `pr-checks.yml` | `pull_request` の `types`（省略時は3種のみ）、`branches` がマージ先と照合されること |
| `nightly-audit.yml` | `schedule`、`failure()` の扱い、`container` / `services`、再利用ワークフローの呼び出し |
| `notify.yml` | `workflow_call` のみの再利用可能ワークフロー（push などでは起動しない） |
| `fan-in-out.yml` | `needs` が分岐して合流する依存グラフ |

## シナリオを追加する

`scenarios.txt` に1行足します。1つ目のフィールドが名前、残りが `gh-sim run` に渡す引数です。

```
push-develop  -event push -ref develop
```

すべてのシナリオには次の引数が先に付くため、必要なら各行で上書きできます。

```
run -dir examples/workflows -all -notes=false
```

追加したら golden を作ります。

```console
make e2e-update
git diff examples/golden   # 期待どおりか確認してからコミットする
```

## golden の役割

判定ロジックを変えたときに、**どのシナリオの結果がどう変わったか**を差分で確認するためのものです。`make test` にも含まれるので、意図しない挙動変更はテストの失敗として現れます。

出力を変える変更を入れたら `make e2e-update` を実行し、`git diff` で差分が意図どおりか確かめてからコミットしてください。
