# ghs の構造

コードを読むときの地図。設計の理由は [design.md](design.md) にある。

## 鍵になる考え方: 同じ形の木が 5 つ

これさえ掴めば残りは追える。**API のパス構造が、そのまま設定ファイルの構造であり、コード内のあらゆる木の構造**になっている。

```mermaid
graph LR
    subgraph API["GitHub API のパス"]
        direction TB
        A1["/repos/{o}/{r}"] --> A2["/actions"]
        A2 --> A3["/permissions"]
        A3 --> A4["/workflow"]
        A1 --> A5["/environments/{name}"]
        A5 --> A6["/variables"]
    end

    subgraph YAML["settings.yml"]
        direction TB
        Y1["(トップレベル)"] --> Y2["actions:"]
        Y2 --> Y3["permissions:"]
        Y3 --> Y4["workflow:"]
        Y1 --> Y5["environments:"]
        Y5 --> Y6["variables:"]
    end

    subgraph CODE["コード内の木"]
        direction TB
        C1["schema.Node<br/>何が書けるか"]
        C2["config.Declaration<br/>何が書かれたか"]
        C3["diff.Plan<br/>何が変わるか"]
        C4["resource.Path<br/>どこへ送るか"]
    end

    API -.同じ形.-> YAML -.同じ形.-> CODE
```

`schema.Node` の `Segment` が「ファイル内のキー」と「API パスの一部」を兼ねている。だから設定の置き場所は、それを変更するエンドポイントを読めば決まる。

## パッケージ

```mermaid
graph TD
    main[main.go] --> cmd

    subgraph cmd["cmd/"]
        init["init.go<br/>木を辿って現在値を書き出す"]
        plan["plan.go<br/>木を辿って差分を組む"]
        apply["apply.go<br/>宣言と差分を辿って書く"]
    end

    cmd --> config
    cmd --> diff
    cmd --> resource

    config["internal/config<br/>settings.yml を読む・検証"]
    schema["internal/schema<br/>設定ファイルの木の定義"]
    diff["internal/diff<br/>比較・差分の木・表示"]
    resource["internal/resource<br/>API の読み書き"]

    config --> schema
    config --> diff
    resource --> schema

    gen["gen/<br/>OpenAPI spec から生成"] -.生成.-> nodes["schema/nodes_gen.go"]
    nodes -.-> schema

    style schema fill:#e8f0fe
    style gen fill:#f5f5f5
```

`diff` はどのパッケージにも依存しない（比較と表示だけ）。`resource` は API を叩くだけで差分を知らない。両者を結ぶのは `cmd`。

## plan の 1 サイクル

```mermaid
sequenceDiagram
    participant U as ユーザー
    participant C as cmd/plan.go
    participant CF as config
    participant S as schema
    participant R as resource
    participant D as diff
    participant GH as GitHub API

    U->>C: ghs plan
    C->>CF: Load(settings.yml)
    CF->>S: Root() で木を取得
    CF->>CF: 木に沿って検証<br/>キー→子ノード / それ以外→フィールド
    CF-->>C: *Declaration（宣言の木）

    loop 宣言された各ノード
        C->>R: Fetch / FetchAll(node, path)
        R->>GH: GET
        GH-->>R: 現在の状態
        R-->>C: リクエストの語彙に揃えた map
        C->>D: Compute / MatchElements
        D-->>C: []Change / []ElementMatch
    end

    C-->>C: *Plan（差分の木）
    C->>D: Render(plan, format)
    D-->>U: text / markdown / json
```

`apply` は同じ `build()` で `Declaration` と `Plan` を得たあと、両方を一緒に辿って書き込む。差分のないノードは飛ばす。

## resource: 大半は書かなくていい

```mermaid
graph TD
    Q{"ノードの種類"}
    Q -->|オブジェクト| O["ObjectFor(key)"]
    Q -->|コレクション| CO["CollectionFor(key)"]

    O --> OG["GenericObject<br/>GET + schema の Method"]
    CO --> CG{"key に特殊実装は？"}
    CG -->|なし| GC["GenericCollection<br/>name でアドレス"]
    CG -->|rulesets| RS["Rulesets<br/>サーバ発行 id でアドレス<br/>要素ごとに個別 GET"]
    CG -->|environments| EN["Environments<br/>報告形→宣言形へ変形<br/>PUT が作成と更新を兼ねる"]

    style OG fill:#e6f4ea
    style GC fill:#e6f4ea
    style RS fill:#fef7e0
    style EN fill:#fef7e0
```

緑が汎用（パスとメソッドだけで動く）、黄が手書き（GitHub が自分のパターンから外れる箇所）。新しい設定を足すとき、多くの場合 `gen/main.go` の `operations` に 1 行足すだけで済む。

## 差分の木

```mermaid
graph TD
    P["Plan<br/>Fields / Elements / Children"]
    P --> F["[]Change<br/>フィールド単位の差分<br/>Label は node 内の相対名"]
    P --> E["[]ElementDiff<br/>要素の create/update/delete"]
    P --> CH["[]*Plan<br/>子ノード"]
    E --> EF["Fields: 更新される要素の変更"]
    E --> EV["Values: 作成/削除される要素の全体"]
    E --> EC["Children: 要素が持つコレクション"]
```

`Change` は「どこにあるか」を持たない。位置は木が持つ。text 出力は木を歩き、markdown / json は `flatten()` で平坦なパスに畳む。

## ファイルの対応

| やりたいこと | 見る場所 |
| --- | --- |
| 管理できる設定を増やす | `gen/main.go` の `operations` |
| 設定ファイルの検証規則 | `internal/config/config.go`, `collection.go` |
| 比較のセマンティクス（配列、null、欠落） | `internal/diff/diff.go` |
| plan の見た目 | `internal/diff/render.go` |
| API 呼び出しの特殊対応 | `internal/resource/rulesets.go`, `environments.go` |
| spec の欠落を埋める | `internal/schema/extra.go` |
