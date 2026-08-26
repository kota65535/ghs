# ghs 設計

GitHub リポジトリ設定を宣言的に管理する小さな CLI ツール。Go 製、[go-gh](https://github.com/cli/go-gh) を利用。

## 基本方針

**settings.yml は REST API のリクエストボディそのもの。** 独自の抽象を挟まない。トップレベルキーが API リソースに対応し、その下のフィールド名・値は GitHub REST API と完全に一致させる。ドキュメントは GitHub の API リファレンスがそのまま使える。

```yaml
# .github/settings.yml
version: 1

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

`repository` の中身は `PATCH /repos/{owner}/{repo}` のボディと 1:1。将来 `rulesets:` を足すときも同じ原則で、対応する API のボディをそのまま書く。

`version` は現状 `1` のみ。それ以外はロード時エラー。

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
}
```

宣言値のキーを起点に、正規化済みの現在値と再帰的に比較する。ネストした map・配列にも同じロジックが効く。

- 配列は**順序込みで比較**をデフォルトとする。順序に意味がないフィールド（Phase 2 の rulesets 内など）は、必要になった時点でフィールド単位の集合比較を導入する
- 宣言キーが GET レスポンスに存在しない場合（権限不足・機能無効等）は Current を `(missing)` として差分扱いで表示する。黙って一致扱いにしない

出力:

```
repository
  ~ allow_auto_merge         false => true
  ~ delete_branch_on_merge   false => true

Plan: 2 to change.
```

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
    resource/
      registry.go
      repository.go
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

注意: `rulesets` や `variables` は「名前をキーにしたコレクション」で、単一オブジェクトの `repository` とは形が違う（作成・更新・削除の 3 操作が要る）。この interface には収まらないので、**コレクション型を実際に足すときに interface を分ける**。今の段階で両対応を作り込まない。

## テスト

- go-gh の RESTClient は interface なので httptest で差し替え可能
- 正規化・差分計算は純粋関数。テーブルテストで固める
- API を叩く処理は GitHub 設定に直撃するため、ここのテストを厚くする

## Phase 1 のスコープ

- `repository` リソースのみ（`PATCH /repos/{owner}/{repo}`）
- `plan` / `apply`
- 単一リポジトリ

対象外（Phase 2 以降）と、その時点で効いてくる設計メモ:

- `rulesets` / `variables` / `environments`: コレクション型。interface 分離、配列の比較セマンティクス、「YAML から消えた要素を削除するか」の方針決定が必要
- `variables` / `secrets`: plan 出力に値が出る。**マスキング方針を実装前に決める**（secrets はそもそも読み出せないので差分検出自体の設計が別途必要）
- `teams`: 比較対象は `GET /orgs/{org}/teams/{slug}/repos` で確認できる**直接付与のみ**に限定する（継承・org ロール経由は対象外）
- `topics`: `PUT /repos/{owner}/{repo}/topics` の別 API。順序に意味がない集合
