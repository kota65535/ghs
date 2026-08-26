# ghs

Keep GitHub repository settings in a file, reviewed like code.

Settings drift: someone flips a merge option in the UI and nobody notices. Put them in `.github/settings.yml` instead — changes go through a pull request, and every repository you manage this way stays consistent.

## Install

With [mise](https://mise.jdx.dev):

```sh
mise use -g ubi:kota65535/ghs
```

Or with Go:

```sh
go install github.com/kota65535/ghs@latest
```

## Use

Write the settings you care about:

```yaml
# .github/settings.yml
version: 1

repository:
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
repository
  ~ allow_auto_merge         false => true
  ~ delete_branch_on_merge   false => true

Plan: 2 fields to change.
```

Apply it:

```sh
ghs apply
```

That is the whole tool. Fields you leave out are not managed, so you can start with two lines and grow the file later.

Authentication comes from `gh auth login` or `GH_TOKEN`, so a local run needs no setup.

## Field names are the API's

Each top-level key is a REST API resource and the fields below it are that resource's fields, spelled exactly as the API spells them — `repository` is the body of `PATCH /repos/{owner}/{repo}`. No mapping table to learn: the [API reference](https://docs.github.com/en/rest/repos/repos#update-a-repository) is the field reference.

Names, types and enum values are checked against GitHub's OpenAPI description before anything is sent, so a typo fails immediately instead of halfway through:

```console
$ ghs plan
ghs: .github/settings.yml: repository.has_discusions: not a writable field of this resource
repository.squash_merge_commit_title: "TITLE" is not one of COMMIT_OR_PR_TITLE, PR_TITLE
```

`repository` is the only resource for now. Rulesets, variables and environments are next.

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
          tool_versions: ubi:kota65535/ghs 0.1.0

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
repository:
  homepage: null
```

Settings changed outside ghs between `plan` and `apply` are overwritten — the file is the source of truth.

## Contributing

```sh
go test ./...
go generate ./...   # refresh the field definitions from the OpenAPI description
```

[DESIGN.md](DESIGN.md) covers how the field definitions are generated and why the tool is built the way it is.
