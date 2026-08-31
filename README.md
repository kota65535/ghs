# ghs

Keep GitHub repository settings in a file, reviewed like code.

Settings drift: someone flips a merge option in the UI and nobody notices. Put them in `.github/settings.yml` instead — changes go through a pull request, and every repository you manage this way stays consistent.

## Install

With [mise](https://mise.jdx.dev):

```sh
mise use -g github:kota65535/ghs
```

Or with Go:

```sh
go install github.com/kota65535/ghs@latest
```

## Use

Write the settings you care about:

```yaml
# .github/settings.yml
has_issues: true
has_wiki: false
allow_squash_merge: true
allow_merge_commit: false
allow_auto_merge: true
delete_branch_on_merge: true
squash_merge_commit_title: PR_TITLE
```

See what would change:

```console
$ ghs plan
~ allow_auto_merge:       false -> true
~ delete_branch_on_merge: false -> true

Plan: 1 to change.
```

Apply it:

```sh
ghs apply
```

That is the whole tool. Fields you leave out are not managed, so you can start with two lines and grow the file later.

Authentication comes from `gh auth login` or `GH_TOKEN`, so a local run needs no setup.

## The file follows the API's paths

The top level of the file is the repository — the body of `PATCH /repos/{owner}/{repo}` — so those fields are written directly. Anything GitHub reaches through a longer path goes under a key named after it:

```yaml
actions:                       # /repos/{owner}/{repo}/actions
  permissions:                 # .../actions/permissions
    enabled: true
    workflow:                  # .../actions/permissions/workflow
      default_workflow_permissions: read
```

**Where a setting goes is decided by the endpoint that changes it.** Read the [API reference](https://docs.github.com/en/rest/repos/repos#update-a-repository) and you know where to write it; there is no mapping table to learn, and field names are spelled exactly as the API spells them.

Names, types and enum values are checked against GitHub's OpenAPI description before anything is sent, so a typo fails immediately instead of halfway through:

```console
$ ghs plan
ghs: .github/settings.yml: has_discusions: not a writable field here
squash_merge_commit_title: "TITLE" is not one of COMMIT_OR_PR_TITLE, PR_TITLE
```

## Actions settings

How Actions runs in the repository — including what a workflow's token is allowed to do:

```yaml
actions:
  permissions:
    enabled: true
    allowed_actions: all                      # all, local_only or selected
    sha_pinning_required: false

    workflow:
      default_workflow_permissions: read      # the GITHUB_TOKEN a workflow gets
      can_approve_pull_request_reviews: false

    fork-pr-contributor-approval:
      approval_policy: first_time_contributors

    artifact-and-log-retention:
      days: 90
```

`default_workflow_permissions` is the one worth pinning. It decides whether every workflow in the repository runs with a token that can write to it, and it is a single click away in the web interface.

Each key is the endpoint that writes the fields under it, which is what keeps `days` legible: on its own it says nothing, under `artifact-and-log-retention` it does.

Some of these only exist under certain conditions — the allowed actions are listed only while `allowed_actions` is `selected`. Declare one where it does not apply and the plan shows it as a change against nothing, rather than failing the run.

## Rulesets, variables and environments

These are sets of named things rather than one object, so they are written as a list. Each entry is the body that creates one, with `name` identifying it:

```yaml
actions:
  variables:
    - name: DEPLOY_REGION
      value: ap-northeast-1

environments:
  - name: production
    wait_timer: 30

rulesets:
  - name: protect-main
    target: branch
    enforcement: active
    conditions:
      ref_name:
        include: ['~DEFAULT_BRANCH']
        exclude: []
    rules:
      - type: pull_request
        parameters:
          required_approving_review_count: 1
```

Writing one of these keys puts the **whole set** under management, so an entry GitHub has and the file does not is deleted:

```console
$ ghs plan
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

Leave the key out and ghs does not touch those settings at all — it does not even ask GitHub what is there. To declare that there should be none, write `rulesets: []`, which does ask for every existing one to be deleted and shows up in the plan as such.

The set is what belongs to the repository itself. Rulesets an organization applies to it, and variables defined at the organization level, are not part of it and are never deleted — ghs cannot write them either way. Anything ghs *can* write is in scope, though, including entries some other tool created, so do not manage the same set from two places.

Within an entry the usual rule holds: fields you leave out are not managed.

An environment's variables go under the environment that owns them:

```yaml
environments:
  - name: production
    wait_timer: 30
    variables:
      - name: DEPLOY_REGION
        value: ap-northeast-1
```

GitHub reaches these through an endpoint of their own, one variable at a time, but that is a detail of how they are written rather than of what they are: they are a field of the environment, so that is where you declare them. The same rules apply as to any list of entries — declaring `variables` manages the whole set, and leaving the key out means ghs does not touch them.

Secrets are not supported, and are not planned. Their values cannot be read back, so a plan could not tell you whether one matches what you declared — and reporting "no changes" without knowing that would be a lie. Use `gh secret set` or a secrets manager.

## In CI

Post the plan on pull requests, apply on merge:

```yaml
# .github/workflows/settings.yml
name: settings
on:
  pull_request:
    paths: ['.github/settings.yml']
  push:
    branches: [main]
    paths: ['.github/settings.yml']

concurrency:
  group: settings

jobs:
  settings:
    runs-on: ubuntu-latest
    permissions:
      pull-requests: write
    steps:
      - uses: actions/checkout@v7
      # Repository settings need admin permission, which GITHUB_TOKEN
      # does not have.
      - uses: actions/create-github-app-token@v3
        id: app-token
        with:
          app-id: ${{ vars.SETTINGS_APP_ID }}
          private-key: ${{ secrets.SETTINGS_APP_PRIVATE_KEY }}
      - uses: jdx/mise-action@v4
        with:
          tool_versions: github:kota65535/ghs 0.1.0

      - if: github.event_name == 'pull_request'
        run: ghs plan --format markdown | gh pr comment "$PR" --body-file -
        env:
          GH_TOKEN: ${{ steps.app-token.outputs.token }}
          PR: ${{ github.event.pull_request.number }}

      - if: github.event_name == 'push'
        run: ghs apply
        env:
          GH_TOKEN: ${{ steps.app-token.outputs.token }}
```

## Reference

```
ghs plan [flags]     show what apply would change
ghs apply [flags]    apply the settings file
```

| Flag | |
| --- | --- |
| `-f`, `--file` | settings file (default `.github/settings.yml`) |
| `-R`, `--repo` | `owner/repo` (default: the current repository) |
| `--format` | `plan` only: `text`, `markdown`, `json` |
| `--exit-code` | `plan` only: exit 2 when there are differences |

`plan` exits 0 even when there are differences, so a pull request check does not fail just because changes are pending. Use `--exit-code` if you want to branch on it.

A value of `null` clears a field:

```yaml
homepage: null
```

For a list-shaped resource `null` is an error instead, because a line left half-written would otherwise declare that every entry be deleted. Write `[]` when that is what you mean.

Settings changed outside ghs between `plan` and `apply` are overwritten — the file is the source of truth.

## Contributing

```sh
go test ./...
go generate ./...   # refresh the field definitions from the OpenAPI description
```

[docs/architecture.md](docs/architecture.md) (Japanese) is a map of the code. [docs/design.md](docs/design.md) (Japanese) covers how the description is generated and why the tool is built the way it is.
