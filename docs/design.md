# ghs 設計

GitHub リポジトリ設定を宣言的に管理する小さな CLI ツール。Go 製、[go-gh](https://github.com/cli/go-gh) を利用。

## 基本方針

**settings.yml は API の語彙でリソースの状態を書いたもの。** 独自の抽象を挟まない。トップレベルキーが API リソースに対応し、その下のフィールド名・値は GitHub REST API と完全に一致させる。ドキュメントは GitHub の API リファレンスがそのまま使える。

```yaml
# .github/settings.yml
repository:
  has_issues: true
  has_projects: true
  has_wiki: true
  has_discussions: false
  allow_squash_merge: true
  allow_merge_commit: true
  allow_rebase_merge: true
  allow_auto_merge: true
  allow_update_branch: true
  delete_branch_on_merge: true
  allow_forking: true
  squash_merge_commit_title: PR_TITLE
  squash_merge_commit_message: PR_BODY
  merge_commit_title: MERGE_MESSAGE
  merge_commit_message: PR_TITLE
  web_commit_signoff_required: false
```

`repository` の中身は `PATCH /repos/{owner}/{repo}` のボディと 1:1 になっている。ただしこれは、リソースの状態が 1 回のリクエストで書き切れる場合にそう見えるというだけで、リクエストボディに合わせているわけではない。複数の API 呼び出しで到達する状態もあり、そのときの書き方は「リストを持つリソースと API の操作単位」の節で扱う。

`version:` のようなフォーマット版のキーは置かない。0.x の段階で互換性を約束しておらず、今は何も区別していない。必要になった時点で「キーが無ければ現行の解釈」として後から足せるので、先回りして書かせる理由がない。

## 設計の核: map ベースで持つ

Go の struct でフィールドを定義**しない**。YAML も API レスポンスも `map[string]any` として扱う。

理由:

- struct では「未指定」と「`false` を明示」が区別できず、全フィールドをポインタ型にする羽目になる
- GitHub が新フィールドを追加するたびに struct を追従させる必要が出る。API リソースベースという方針と矛盾する

### 管理のセマンティクス

- **キーが無い** → 非管理。そのフィールドには一切触れない（部分管理）
- **キーがある** → 管理対象。値を PATCH ボディに verbatim で含める
- **`null` も値の一つ**。そのまま送る。`homepage: null` は設定済み homepage のクリアを意味する

`key:`（値省略）は YAML 仕様上 `null` と等価で、区別できない。書きかけ放置による意図しないクリアは、plan の差分表示（`"..." => null`）が PR レビューで必ず見えることで防ぐ。

### 型の正規化

YAML パーサは数値を `int`、`encoding/json` は `float64` にデコードするため、素朴比較では `1 != 1.0` となり永続差分（apply しても消えない差分）が発生する。

対策: desired（YAML 由来）を **JSON 往復で正規化**してから比較する。

```go
func normalize(v map[string]any) (map[string]any, error) {
    b, err := json.Marshal(v)
    if err != nil {
        return nil, err
    }
    var out map[string]any
    err = json.Unmarshal(b, &out)
    return out, err
}
```

これでネストした map・配列・数値がすべて API レスポンスと同じ型体系（`float64` / `string` / `bool` / `nil` / `[]any` / `map[string]any`）に揃い、`reflect.DeepEqual` で比較できる。current は元々 `encoding/json` 産なので変換不要。

2^53 超の整数は float64 で精度が落ちるが、GitHub 設定値に該当なし（巨大な数値は `id` 系 = 書き込み不可フィールドのみ）。

YAML パーサは `map[string]any` を返す yaml.v3（または goccy/go-yaml）を使う。yaml.v2 の `map[any]any` は JSON 往復できない。

## フィールド検証: OpenAPI 定義から生成

書き込み可能フィールドの判定は手書きしない。**GitHub 公式 OpenAPI 定義（[github/rest-api-description](https://github.com/github/rest-api-description)）の `PATCH /repos/{owner}/{repo}` requestBody スキーマ**が書き込み可能フィールドの完全な列挙になっている。

- `go generate` で spec から properties を抽出し、リソースごとの許可フィールドリスト（＋型・enum）を Go ファイルに生成する
- spec はコミット SHA でピンし、Renovate に更新させる。手書き allowlist の追従問題を自動化で解消
- リソース追加時（rulesets 等）も operation を指定するだけで同じ仕組みが効く

ロード時の検証:

- 許可リスト外のキー → **エラー**（タイポ検出）。例外の仕組みは持たない。後述の補完リストで spec の欠落を埋める
- enum フィールドの不正値 → エラー
- 型の不一致 → エラー
- **`null` は検証しない。** そのフィールドが null を受け付けるかは spec から一貫して読み取れない。リクエストボディのスキーマには nullable がほとんど記録されておらず、レスポンス側で補おうとしても、リソースによってはリクエストとレスポンスでフィールド名が対応しない（`PUT /repos/{owner}/{repo}/environments/{name}` の `wait_timer` / `reviewers` は、レスポンスの `environment` スキーマに同名で存在しない）。誤って弾くと値をクリアする手段が完全に失われる一方、通してしまっても plan に `true => null` と表示され、API に届けば 422 で落ちる。判断は API に任せる

エラーは最初の 1 件で止めず、ファイル全体分をまとめて報告する。

### spec の欠落は補完リストで埋める

spec に載っていないが API は受け付ける、というフィールドが存在する。`has_discussions` は `PATCH /repos/{owner}/{repo}` で受理されることを実測で確認したが、リクエストボディのスキーマにも REST ドキュメントにも記載が無い（`POST /user/repos` と GET のレスポンススキーマには存在する）。Discussions の GA から 3 年経ってこの状態なので、更新待ちで解決するとは考えない。

これを検証の逃げ道（`--allow-unknown` のようなファイル単位のスイッチ）で処理すると、タイポ検出が常時無効になる。代わりに `internal/schema/extra.go` に手書きの補完リストを置き、生成物にマージする。

- 補完したフィールドも型・enum の検証対象になる
- 実測で確認したものだけを入れる。方針判断は含めない（削除した denylist との違い）
- spec が追いついたら不要になる。テストが検出して落ちるので、そこで消す
- 生成側とマージ側を別の変数（`generated` と `resources`）に分け、補完が生成物に混ざらないようにする

現時点の内容は 1 件。同種の候補として `has_downloads`（2012 年に廃止された機能）があるが、実用性が無いので入れていない。リストが育つようなら、生成方式そのものを見直す。

### 影響の大きいフィールドを禁止しない

`PATCH /repos` は `name`（リネーム）、`private` / `visibility`（公開は不可逆）、`archived`、`default_branch` も受け付ける。これらを denylist で拒否する案を検討したが、採らない。

非管理が既定であることが、そのまま防御になっている。settings.yml に書かなければ ghs は触れない。書いた場合は plan に差分として現れ、PR レビューを通る。ツール側で追加の禁止リストを持つのは二重の防御になるが、その分「API に書けるものは書ける」という原則が崩れ、リストの維持も必要になる。

## 差分計算

```go
type Change struct {
    Path    string // "repository.allow_auto_merge"
    Current any
    Desired any
    Action  Action // create / update / delete
    Element string // コレクションの要素名。単一オブジェクトでは空
}
```

宣言値のキーを起点に、正規化済みの現在値と再帰的に比較する。ネストした map・配列にも同じロジックが効く。

- 配列は**順序込みで比較**する。要素を index で対にして再帰するので、報告の粒度は `rules[0].parameters.x` のような葉になる。長さが違っても共通する位置は同じ扱いで、片側にしか無い位置だけを追加・削除として報告する（詳細は Phase 2 の節）
- 宣言キーが GET レスポンスに存在しない場合（権限不足・機能無効等）は Current を `(missing)` として差分扱いで表示する。黙って一致扱いにしない

出力の様式は Terraform の plan に倣う。行頭の記号で操作を示し、`->` で変更前後を結ぶ。ただしフィールドと値の区切りは `=` ではなく `:` にする。Terraform が `=` なのは HCL がそうだからであって、ghs の入力は YAML だから `:` のほうが読み手の頭にある形に近い。

```
~ repository:
    ~ allow_auto_merge:       false -> true
    ~ delete_branch_on_merge: false -> true

Plan: 1 to change.
```

**1 リソース 1 ブロック**とし、ブロックの中身は settings.yml でそのリソースを書くときの形に合わせる。単一オブジェクトならフィールドの並び、コレクションなら要素の並びになる。

数える単位はオブジェクト、すなわち単一オブジェクトリソースそのものかコレクションの 1 要素であって、フィールドではない。1 リソースのフィールドがいくつ変わっても「1 to change」で、コレクションの 1 要素と同じ重みになる。フィールド単位の内訳は本文にすべて出ているので、要約で二重に数える意味がない。

- TTY のときだけ色付け
- `--format markdown`: PR コメント用。CI から `gh pr comment` に流す
- `--format json`: 機械可読。CI での分岐・集計用

## apply

差分があるリソースだけ API を呼ぶ。送るのは差分ではなく**宣言値全体**。PATCH は冪等なので単純な方を採る。

plan と apply の間に手動変更が入った場合、apply は宣言値で上書きする（TOCTOU）。Terraform の plan ファイルに相当する仕組みは持たない。宣言が常に正 — これは許容するリスクとして明記しておく。

## CLI

```
ghs plan  [-f .github/settings.yml] [-R owner/repo] [--format text|markdown|json] [--exit-code] [--allow-unknown]
ghs apply [-f .github/settings.yml] [-R owner/repo] [--allow-unknown]
```

`-R owner/repo` は gh の慣習に合わせる。省略時は go-gh の `repository.Current()` で git remote から解決。

終了コード:

| コマンド | 差分なし | 差分あり | エラー |
| --- | --- | --- | --- |
| `plan` | 0 | 0（`--exit-code` 指定時は 2） | 1 |
| `apply` | 0 | 0 | 1 |

差分ありでも 0 を返すのは、PR チェックを差分の有無で落とさない既定方針から。分岐したい CI 向けに `--exit-code` を用意する（`terraform plan -detailed-exitcode` と同じ慣習）。

## 認証

go-gh の `api.DefaultRESTClient()` に任せる。`GH_TOKEN` / `GITHUB_TOKEN` と `gh auth login` の認証情報を自動解決するため、ツール側に認証コードを書かない。

トークンをログ・エラーメッセージに出力しない。

## CI での利用（利用側の設計）

ツール本体のスコープ外だが、想定する運用を README に残す:

- **plan**: PR トリガー。結果を `--format markdown` で PR コメントに投稿
- **apply**: main への merge トリガー
- リポジトリ設定の PATCH には admin 権限が必要。`GITHUB_TOKEN` では不足するため **GitHub App の installation token** を使う
- apply ワークフローには `concurrency` グループを設定し、同時実行を防ぐ

## パッケージ構成

```
ghs/
  main.go
  cmd/
    root.go
    plan.go
    apply.go
  internal/
    config/       settings.yml のロードと検証
    schema/       生成されたフィールド定義（許可フィールド・型・enum）
    resource/
      resource.go     単一オブジェクトの interface とレジストリ
      repository.go
      collection.go   コレクションの interface と共通処理
      variables.go
      rulesets.go
      environments.go
    diff/         正規化・差分計算・表示
  gen/            OpenAPI spec からのフィールド定義生成（go generate）
```

リソースは interface で抽象化し、registry に登録する:

```go
type Resource interface {
    Name() string // "repository"
    Fetch(ctx context.Context, r Repo) (map[string]any, error)
    Apply(ctx context.Context, r Repo, desired map[string]any) error
    Schema() FieldSchema // 生成コード由来（許可フィールド・型・enum・nullable）
}
```

注意: `rulesets` や `variables` は「名前をキーにしたコレクション」で、単一オブジェクトの `repository` とは形が違う（作成・更新・削除の 3 操作が要る）。この interface には収めず、別の interface を切る（Phase 2 の節を参照）。

## テスト

- go-gh の RESTClient は interface なので httptest で差し替え可能
- 正規化・差分計算は純粋関数。テーブルテストで固める
- API を叩く処理は GitHub 設定に直撃するため、ここのテストを厚くする

## Phase 1 のスコープ

- `repository` リソースのみ（`PATCH /repos/{owner}/{repo}`）
- `plan` / `apply`
- 単一リポジトリ

## Phase 2: コレクション型リソース

対象は `variables` / `rulesets` / `environments`。いずれも「名前を持つ要素の集まり」であり、単一オブジェクトの `repository` と違って作成・更新・削除の 3 操作が要る。secrets は対象外とする（理由は後述）。

対応する API:

| リソース | 一覧 | 作成 | 更新 | 削除 |
| --- | --- | --- | --- | --- |
| `variables` | `GET /repos/{o}/{r}/actions/variables` | `POST .../actions/variables` | `PATCH .../actions/variables/{name}` | `DELETE .../actions/variables/{name}` |
| `rulesets` | `GET /repos/{o}/{r}/rulesets` | `POST .../rulesets` | `PUT .../rulesets/{ruleset_id}` | `DELETE .../rulesets/{ruleset_id}` |
| `environments` | `GET /repos/{o}/{r}/environments` | `PUT .../environments/{name}`（upsert） | 同左 | `DELETE .../environments/{name}` |

一覧の取得には 2 つ注意点がある。

`GET /repos/{o}/{r}/rulesets` は既定で `includes_parents=true` で、org が当該リポジトリに適用しているルールセットまで返す。これは ghs では管理できないので、**必ず `includes_parents=false` を付ける**。付け忘れると毎回の plan がそれらの削除を提案することになる。

また、一覧のレスポンスに `rules` と `conditions` が含まれるかは OpenAPI 定義から確定できない。含まれない場合、宣言したルールがすべて `(missing)` として差分になる。そこで一覧で id を得たあと**要素ごとに個別 GET する**。ルールセットは通常数個なのでリクエスト数は問題にならない。

### YAML の形: name を持つ要素の配列

コレクションは、作成 API のリクエストボディをそのまま並べた配列として書く。

```yaml
variables:
  - name: DEPLOY_REGION
    value: ap-northeast-1

rulesets:
  - name: protect-main
    target: branch
    enforcement: active
    conditions:
      ref_name:
        include: ["~DEFAULT_BRANCH"]
        exclude: []
    rules:
      - type: pull_request
        parameters:
          required_approving_review_count: 1

environments:
  - name: production
    wait_timer: 30
```

`variables` と `rulesets` の要素は作成 API のボディと 1:1 で、「リクエストボディそのもの」の原則がそのまま成り立つ。`environments` だけは name がボディではなくパス（`PUT .../environments/{name}`）に入るため、要素に `name` を追加で要求する。名前をキーにした map で書く形（`environments:` 側に寄せる形）も検討したが、その場合は `variables` / `rulesets` の 2 リソースでボディから name を抜く変形が要る。逸脱が 1 リソースの「フィールド追加」で済む配列形を採る。

- `name` は全要素で必須。欠落・重複はロード時エラー
- 配列の並び順に意味はない。要素は name で突き合わせる
- `rulesets: []`（空配列）は「ルールセットを 1 つも持たない」という宣言。既存の全要素が削除対象になる
- `rulesets:`（値省略 = null）はエラーにする。単一オブジェクト側では null を「クリア」として通しているが、コレクションでは書きかけ 1 行が全削除の宣言になってしまう。空配列という明示的な書き方があるので、null に意味を与えない

### キーを書いたら集合全体を管理する

YAML から消えた要素は**削除する**。トップレベルキーの宣言は、コレクションの場合「この集合の全メンバーを管理する」という意味になる。

要素単位で「書いてないものは触れない」とする案（追加専用）は採らない。追加専用では要素のリネームが旧名の残骸を生み、宣言と実態が収束しない。「宣言が常に正」が集合の中身については成り立たなくなる。

非管理を既定とする防御は、フィールドのときと同じくキーの粒度で効いている。`rulesets:` を書かなければ ghs は既存ルールセットに一切触れず、書いた場合の削除は plan に削除として必ず現れ、PR レビューを通る。

要素の**中**では Phase 1 と同じ部分管理が成り立つ。宣言した要素の宣言したキーだけを比較し、API が返す読み取り専用フィールド（`id`、`created_at` 等）は宣言に無いので自然に無視される。

### 「全要素」が指す範囲

集合の範囲は **FetchAll が返すもの**、すなわちそのリポジトリに直接属する要素に限られる。継承されたものや上位スコープのものは含まれない。

- `rulesets`: org がこのリポジトリに適用しているルールセットは含めない（`includes_parents=false`）。ghs はこれらを変更できないので、集合に入れれば毎回の plan が削除を提案し続けることになる
- `variables`: `GET /repos/{o}/{r}/actions/variables` が返すのはリポジトリレベルの変数のみ。org レベルの変数は別 API で、対象外
- `environments`: 上位スコープの概念がないため、返るものすべてが対象

「宣言が正」は、**ghs が書き込める範囲について**成り立つ。書き込めないものを削除対象に数えないのは方針の例外ではなく、方針が適用される範囲の定義である。`teams` を直接付与のみに限る判断（Phase 3）も同じ考え方に立つ。

一方、ghs 以外の手段（Terraform、手作業、他のツール）で作られた要素は、書き込める範囲にある以上、削除対象になる。同じリソースを二重に管理しないことは利用側の責任になる。

### interface 分離

単一オブジェクト用の `Resource` とは別に、コレクション用の interface を切る。

```go
type Collection interface {
    // Name is the top-level key in settings.yml.
    Name() string

    // FetchAll returns current elements shaped like the create request body,
    // keyed by element name. Read-only fields the API reports (id etc.) are
    // kept so Update and Delete can use them.
    FetchAll(ctx context.Context, c Client, repo Repo) (map[string]map[string]any, error)

    Create(ctx context.Context, c Client, repo Repo, desired map[string]any) error

    // Update receives current alongside desired because some collections
    // address elements by a server-issued id (rulesets) rather than by name.
    Update(ctx context.Context, c Client, repo Repo, current, desired map[string]any) error

    Delete(ctx context.Context, c Client, repo Repo, current map[string]any) error
}
```

FetchAll が「作成リクエストの語彙」に揃えて返すのがポイント。`environments` の GET レスポンスは `wait_timer` / `reviewers` / `prevent_self_review` を `protection_rules` 配列の中に畳み込んでおり、リクエストボディと形が一致しない（検証の節で触れた非対称と同じもの）。レビュアーも、レスポンスは `{type, reviewer: {id, login, ...}}` だがリクエストは `{type, id}` で、ここでも形が違う。この変形はリソース実装の中に閉じ込め、diff 層には Phase 1 と同じ「同じ語彙の map 同士の比較」だけをさせる。

変形では、**オフの保護ルールをオフを意味する値として補う**必要がある。`protection_rules` は有効なルールしか含まないため、待機時間を設定していない environment のレスポンスには `wait_timer` が現れない。補わずに変換すると、`wait_timer: 0`（＝待機なし）という宣言が `(missing) => 0` の差分として現れ、apply しても消えない。`prevent_self_review` は `false`、`reviewers` は空配列が対応する既定値になる。

「レスポンスをリクエストの語彙に戻す」とは、フィールド名を移し替えるだけでなく、**ルールの有無を値の有無ではなく値そのものとして表現し直す**ことでもある。

`environments` は作成と更新が同一リクエスト（`PUT .../environments/{name}`）なので、Create と Update の実装は同じものになる。interface 上は分けたままにする。呼び出し側が「作成なのか更新なのか」を知っているのは plan の表示と apply の順序制御に必要で、その区別を API の都合で潰す理由がない。

一覧はページネーションを回す。go-gh の REST クライアントはレスポンスヘッダを返さないため Link ヘッダが読めないので、「1 ページの件数が上限未満なら最終ページ」で判定する。`variables` の一覧は per_page 上限が 30、他は 100。API が最終ページを返し続けない事故に備えて 100 ページで打ち切る。

差分は要素単位の操作として表す:

- 宣言にあって現状に無い name → **create**
- 両方にある name → 宣言キーだけを再帰比較し、差分があれば **update**（送るのは Phase 1 と同じく宣言値全体）
- 現状にあって宣言に無い name → **delete**

apply の実行順は create → update → delete で固定する。途中で失敗したとき、残るのが「余分な要素」（未削除）であって「欠けた要素」にならないようにする。

出力:

コレクションは**シーケンスとして**出す。settings.yml で配列として書くものを、plan だけ `rulesets["protect-main"]` のようなキー付きの形で見せると、入力と出力で構造が食い違う。名前をキーに突き合わせているのは内部の話であって、読み手が知っていることではない。

```
~ rulesets: [
    - {
        - enforcement: "active"
        - id:          7
        - name:        "legacy"
      },
    ~ {
          name:        "protect-main"
        ~ enforcement: "evaluate" -> "active"
      },
    + {
        + conditions: {
            + ref_name: {
                + include: [
                    + "~DEFAULT_BRANCH",
                  ]
              }
          }
        + enforcement: "active"
        + name:        "release-protect"
        + target:      "branch"
      },
  ]

Plan: 1 to create, 1 to change, 1 to delete.
```

要素そのものに記号が付く。リソースの記号が常に `~` なのは、リポジトリは存在していて、生まれたり消えたりするのは中の要素だからである。

create と delete では要素のフィールドを並べ、入れ子の map と配列は展開する。何が作られ何が消えるのかは中身を見なければ判断できず、`rules` のような入れ子を 1 行の JSON で出しても読めない。delete では現在値をそのまま出すので `id` や `created_at` のような読み取り専用フィールドも並ぶ。冗長ではあるが、実際に消えるものの実態がそれである以上、省く理由がない。

update では、変更されたフィールドの前に `name` を**記号なしで**置く。シーケンスの要素には見出しがないので、これがないとどの要素の話か分からない。記号を付けないのは、`name` が変更の一部ではなく要素を指すための文脈だからで、Terraform が `id` のような不変のフィールドをそのまま並べるのと同じ扱いになる。

Terraform はリストの要素を追跡しないので、中身が変わった要素は削除と追加のペアになる。ghs は `name` で追跡するため要素単位の update があり、そこだけは Terraform に対応する形がない。

入れ子は `{ }` と `[ ]` で囲む。YAML のブロックスタイル（インデントのみ、配列は `- `）に寄せる案もあるが、配列マーカーの `-` が削除を示す `-` と衝突して誤読を招く。flow style は YAML として正しい記法なので、これで表記の一貫性は保たれる。

update のフィールドは `rules[0].parameters.x: 1 -> 2` のような平坦なパスで出す。木として展開する手もあるが、差分のパスから木を組み直す必要があるうえ、深い入れ子の中で変更箇所を探すことになる。「どこが変わったか」を読む用途では平坦なパスのほうが速い。create と delete で展開するのは、そこでは値そのものが読む対象だからで、目的が違う。

片側にしか値が無い差分（配列の伸縮）は、`->` の片側が空になるので、記号を `+` / `-` にして値だけを出す。値がオブジェクトや配列なら展開する。ルールが 1 つ消えるだけで数十行分の JSON が 1 行に潰れるのを避けるため。

```
        ~ enforcement:   "active" -> "evaluate"
        ~ rules[1].type: "non_fast_forward" -> "required_linear_history"
        - rules[2]: {
            - parameters: {
                - required_approving_review_count: 0
              }
            - type: "pull_request"
          }
```

削除される要素には、宣言していない読み取り専用フィールドや既定値も並ぶ。要素ごと消える以上それが実態なので、ここでは省かない。共通位置の比較で無視されるのとは事情が違う。

なお差分の `Path`（`rulesets["protect-main"].enforcement`）はキー付きのままにする。`--format json` と検証エラーが使うもので、機械が読む識別子としては、順序に左右されず削除された要素にも与えられるキー表記のほうが扱いやすい。テキスト出力だけを人が読む形に寄せる。

markdown の表は `Action | Path | Current | Desired` の 4 列で、create と delete の行には要素全体が入る。

### 配列の比較: index 対で再帰する

Phase 1 の「配列は順序込みで `DeepEqual`」はコレクションで破綻する。ルールセットの `rules` は要素がオブジェクトの配列で、サーバは宣言に無い `parameters` の既定値を埋めて返す。配列全体の `DeepEqual` では、宣言に `required_approving_review_count` だけ書いたルールが、サーバの埋めた既定値のせいで永続差分になる。

そこで配列の比較を次に改める:

- index で対にして再帰する。対がともにオブジェクトなら宣言キーだけの比較（map と同じ walk）、それ以外は `DeepEqual`
- 長さが違っても、**共通する位置については同じ扱い**にする。片側にしか無い位置だけを、追加または削除として個別に報告する

長さが違う場合に配列全体を 1 件の差分とする案を最初に採ったが、これは誤りだった。ルールを 1 つ減らしただけで、残るルールに GitHub が埋めた既定値まで「削除される」と表示されてしまう。宣言していないフィールドは管理対象外なのだから、これは嘘の報告になる。長さが同じときは宣言キーだけを見るのに、長さが違うと要素まるごとを比較するという不整合が原因だった。

片側にしか無い位置は `CurrentMissing` / `DesiredMissing` で表す。`DesiredMissing` は配列が短くなる場合にだけ立つ。宣言に無いフィールドは差分にすらならないので、この 2 つは別のものを指す。

これは「宣言したキーだけを見る」という部分管理のセマンティクスを配列の中まで通す変更であり、順序が意味を持つことは変わらない。apply は宣言値全体を送るので、サーバが並びを保存する限り順序は宣言と一致する。サーバ側で並び替えが起きるフィールドが実測で見つかったら、そのフィールドに限って name や type をキーにした突き合わせを導入する。

差分のパスは `rulesets["protect-main"].rules[0].parameters.required_approving_review_count` のように index で示す。順序を入れ替えただけの配列は、対応する index がすべて差分として報告される。

### フィールド定義の生成: $ref を追う

`rulesets` のリクエストボディは `$ref` でスキーマを共有している。`enforcement` は `#/components/schemas/repository-rule-enforcement` を指し、`conditions` も同様に外部のスキーマを指す。現行の生成器は `$ref` を解決しないため、そのままでは enum も入れ子のプロパティも取り逃がし、API が拒否する値を検証が通してしまう。

そこで生成器に `$ref` の解決を入れる。ローカル参照（`#/` 始まり）だけを対象とし、たどった参照を記録して循環したら打ち切る。`environments` の `wait_timer`（`integer`）や `prevent_self_review`（`boolean`）も、これで型が取れるようになる。

解決できない参照に当たったフィールドは型なし、つまり検証なしで通す。ここで弾くと API が受け付ける設定を書けなくなるので、判断は API に任せるという既定方針に従う。

ルールセットの `rules` の要素は 23 通りの `oneOf` で、それ自体の `properties` を持たない。したがって「properties を宣言しないオブジェクトは自由形式」という既存の規則がそのまま効き、ルールの中身の検証は API に委ねられる。

### variables はマスキングしない

variables の値は plan 出力に平文で出す。

値は settings.yml に平文で書かれており、PR の時点で既に見えている。GitHub 自身が variables を「非機密の設定値のため」と定義し、UI でもログでも平文で扱う。隠すべき値がそもそも無い一方、マスキングすると差分レビュー（値のタイポや貼り間違いの発見）ができなくなる。機密値は variables ではなく secrets に置くのが GitHub の想定であり、ghs もその線引きに乗る。

### secrets は管理しない

secrets は Phase 2 でも対象外とし、当面サポートしない。宣言的管理の前提が二重に成り立たないため。

- **値を YAML に書けない。** settings.yml は PR でレビューされる平文ファイルであり、シークレットの平文コミットになる
- **差分検出ができない。** GET は名前と更新日時しか返さず、値は読み出せない。宣言と現状の比較が値について不可能

名前だけの存在管理（値は apply 時に環境変数から注入）という折衷案も検討したが、値のドリフトを plan が検出できない以上、「差分なし」という plan の報告が値については何も保証しない。冪等性の報告が嘘になる管理は載せない。secrets の投入は `gh secret set` や Actions の専用ステップに任せる。

### 実測してから決める点

実装は確定させたが、実際の API に対して確認したい点が残っている。

**variables の name の大文字小文字。** 参照時は大文字小文字を区別しないと GitHub が明言している一方、API が返す表記が登録時のものか正規化されたものかは確認できていない。仮に GitHub が大文字化して保存するなら、小文字で宣言したユーザには毎回 delete + create の plan が出る。今は完全一致で突き合わせている。突き合わせ用のキーを畳む仕組みは interface を増やすので、実測してから必要なら入れる。plan が先に見えるので、気付かないまま壊れることはない。

**サーバが配列の並びを保存するか**（`rules`、`bypass_actors`、`conditions.ref_name.include`）。並び替えられるフィールドがあれば、そのフィールドだけ集合として突き合わせる必要がある。

**rulesets 一覧が `rules` / `conditions` を含むか。** 含むと分かれば要素ごとの GET を省ける。今は含まない前提で実装している。

## リストを持つリソースと API の操作単位

リソースがフィールドとしてリストを持つとき、API の作り方は 2 通りある。

- **更新のたびに完全なリストを渡す。** `allowed_merge_methods` や `rules`、`reviewers` がこれにあたる
- **リストの要素の追加・更新・削除に別の API を用意する。** variables や environment の variables がこれにあたる

前者は要素が少なく短いリストに向く。長くなりうるリスト、あるいは要素ごとに権限や監査を分けたいリストでは後者が選ばれる。environment の variables に `POST /repos/{o}/{r}/environments/{env}/variables` という独立したパスがあるのはそのためで、**リストが environment のフィールドでなくなったわけではない**。

したがって settings.yml では、どちらの API でも同じように書く。

```yaml
environments:
  - name: production
    wait_timer: 30
    variables:
      - name: DEPLOY_REGION
        value: ap-northeast-1
```

`variables` は `PUT /repos/{o}/{r}/environments/{environment_name}` のリクエストボディには無い。それでも environment の状態の一部であり、宣言する側にとって `wait_timer` と区別する理由がない。差分を要素ごとの API 呼び出しに翻訳するのは ghs の仕事である。

### 1 つの状態が複数のエンドポイントに散っている場合

前節はリソースがリストを持つ場合だったが、リストでなくても同じことが起きる。リポジトリの Actions 設定は 4 つのパスに分かれている。

- `PUT /repos/{o}/{r}/actions/permissions`: `enabled`、`allowed_actions`、`sha_pinning_required`
- `PUT .../actions/permissions/workflow`: `default_workflow_permissions`、`can_approve_pull_request_reviews`
- `PUT .../actions/permissions/fork-pr-contributor-approval`: `approval_policy`
- `PUT .../actions/permissions/selected-actions`: `github_owned_allowed` ほか

宣言する側から見れば「このリポジトリで Actions がどう動くか」という 1 つの状態なので、`actions:` の下に平坦に並べる。生成器の `operations` は同じリソース名の行を複数許し、フィールドはそれらの和になる。

```yaml
actions:
  enabled: true
  allowed_actions: all
  sha_pinning_required: false
  default_workflow_permissions: read
  can_approve_pull_request_reviews: false
```

apply は、宣言されたフィールドを持つエンドポイントだけを呼ぶ。順序は `permissions` を先にする。どの action を選ぶかは、方針が `selected` になって初めて意味を持つため。

フィールドとエンドポイントの対応表は `internal/resource/actions.go` に手書きする。生成されるフィールド定義との食い違いは、「生成された全フィールドがどれかのエンドポイントに属する」ことを確かめるテストで検出する。これがないと、GitHub が足したフィールドが settings.yml では受理されるのに送られない、という状態になる。

#### 条件付きで存在しないエンドポイント

`selected-actions` は `allowed_actions` が `selected` のときにしか存在せず、それ以外では GET が 409 を返す。同様に private リポジトリ限定の設定は public で 422 になる。

これらは失敗ではなく「そこには何も無い」という応答なので、該当フィールドを現在値から落として続行する。宣言していれば `(missing)` として差分に出る。これは「宣言キーが GET レスポンスに存在しない場合は差分扱いにする」という既定の方針がそのまま効く形になっている。

403 や 5xx は区別してエラーとして伝える。「設定が適用されない」ことと「読めない」ことは違う。

#### 入れないと決めたエンドポイント

`actions/permissions/artifact-and-log-retention` は入れない。唯一のフィールドが `days` という名前で、`actions:` の下に他のフィールドと並べると何の日数か読み取れない。API がパスの側に文脈を寄せている例であり、「API の語彙をそのまま書く」という方針は、フィールド名がその文脈を持ち歩いていることを前提にしている。前提が崩れる場合まで無理に適用しない。

`actions/permissions/access` と `fork-pr-workflows-private-repos` も外した。private / internal 限定で、public リポジトリでは実測できないため。条件付きの仕組みには乗るので、必要になれば行を足すだけで入る。

### 原則の言い直し

「settings.yml は REST API のリクエストボディそのもの」という言い方は、Phase 1 の `repository` が 1 つの PATCH に対応していたから成り立っていた。コレクションを入れた時点でこれは正確でなくなっている。

- `environments` の `name` はパスに入るので、どのリクエストボディにも存在しない
- コレクションのトップレベルキー 1 つが、一覧・作成・更新・削除の 4 操作に対応する

正確には、**トップレベルキーは管理対象のリソースを指し、その下はそのリソースを記述する API の語彙で書く**。何回 API を叩いてその状態に到達するかは実装の側の問題であって、宣言する側が知る必要はない。独自の抽象を挟まないという方針は変わらない。抽象を挟まない対象が「1 回のリクエスト」ではなく「リソースの状態」だというだけである。

この読み方は Phase 3 の判断にも効く。`topics` は `PUT /repos/{o}/{r}/topics` という別の API を持つが、リポジトリの状態の一部なので `repository` の下に書ける。

### 入れ子のコレクション

`environments` の `variables` は、要素の中にあるコレクションとして実装する。

**フィールド定義**は生成側で対応する。`gen/main.go` の `nestedOperations` に「どのリソースの、どのフィールドが、どの作成 API に対応するか」を書き、`POST /repos/{o}/{r}/environments/{environment_name}/variables` のリクエストボディから要素のフィールドを生成する。`schema.Field` に `Elements` を持たせ、非 nil なら入れ子コレクションとする。

**検証**は外側と同じ規則を適用する。`name` 必須、重複エラー、`variables:`（null）はエラー、`variables: []` は全削除の宣言。トップレベルのコレクションと入れ子で規則が違う理由がないので、要素の検証は共通の関数に切り出す。

**差分**では、入れ子のフィールドを通常の比較から除外し、name で突き合わせて別に比較する。パスは `environments["production"].variables["DEPLOY_REGION"].value` の 2 段になる。`Change` には親要素を指す `Element` に加えて、入れ子のフィールド名 `Nested` とエントリ名 `Entry` を持たせる。パスにも同じ情報は入っているが、表示のたびに自分の吐いたパスを読み返すことになるため、構造のまま持つ。

入れ子の要素の増減は、トップレベルのような create・delete では**なく** update として扱う。片側にしか無い値（`CurrentMissing` / `DesiredMissing`）として表す。environment の変数が増えることは environment の変更であって、変数が独立したオブジェクトとして生まれるわけではない。ここを create として数えると「1 to create」が変数の数だけ出て、要約がリソースの数を語らなくなる。

**表示**も外側と同じシーケンス形式にする。入れ子だけキー付き（`variables["X"]`）で出すと、settings.yml には無い形を見せることになる。

```
~ environments: [
    ~ {
          name: "production"
        ~ variables: [
            ~ {
                  name:  "REGION"
                ~ value: "ap-northeast-1" -> "us-east-1"
              },
            + {
                + name:  "ADD"
                + value: "new"
              },
          ]
      },
  ]
```

要素とエントリは同じ構造なので、描画も同じ関数が自分を呼ぶ形で書ける。

**apply** は `Environments` の中に閉じ込める。`Create` と `Update` がどちらも「environment 本体を PUT してから変数を揃える」という同じ手順になるので、実体は 1 つにまとめる。変数の順序は create → update → delete で、トップレベルと同じく途中で失敗したときに余分が残る側に倒す。environment を先に作るのは、存在しない environment に変数を入れられないため。

`Collection` interface は変えていない。入れ子は「そのリソースをどう更新するか」の内側の話であって、コレクションの扱い方そのものは変わらないため。

要素の識別は各段で `name` のままでよい。変数は environment と名前の組で決まるが、入れ子の中では親が既に決まっている。

## Phase 3 以降

- `teams`: 比較対象は `GET /orgs/{org}/teams/{slug}/repos` で確認できる**直接付与のみ**に限定する（継承・org ロール経由は対象外）
- `topics`: `PUT /repos/{owner}/{repo}/topics` の別 API だが、リポジトリの状態なので `repository` の下に書く。順序に意味がない集合なので、比較には集合としての扱いが要る
