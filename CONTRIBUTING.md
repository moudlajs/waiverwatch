# Contributing

Solo project: the owner is product owner and reviewer, Claude implements.
The rules still apply to both.

## Flow

1. **Issue first.** Every change has an issue with a milestone, and a
   `type:`, `area:` and `priority:` label. Use the issue forms.
2. **One issue = one branch = one draft PR.**
   Branch: `<type>/<issue-number>-<short-slug>`, e.g. `feat/4-list-leagues-tool`.
3. **Open the PR as a draft** with `Closes #N` in the body.
4. **Mark it ready only when CI is green.** That triggers the Claude review.
   Resolve every review conversation before merging.
5. **Squash merge.** The PR title becomes the commit on `main`.

## PR titles

[Conventional Commits](https://www.conventionalcommits.org/), checked in CI:
`feat`, `fix`, `chore`, `docs`, `test`, `ci`, `build`, `refactor`, `perf`.
A scope is optional: `feat(mcp): add get_matchup tool`.

Versions come from these titles, never from milestones: `feat` bumps the
minor version, `fix` the patch. release-please opens a release PR; merging it
tags and releases. Milestones are for planning only.

## Dependencies

No update bots. Dependencies are bumped by hand during maintenance passes,
preferring releases that are at least a few weeks old (supply-chain safety).
GitHub's vulnerability alerts stay on and show up in the Security tab.

## Checks

```sh
golangci-lint run           # lint + gofmt/goimports (config: .golangci.yml)
go test -race ./...         # tests never hit the real Sleeper API
go build ./...
govulncheck ./...           # go install golang.org/x/vuln/cmd/govulncheck@latest
```

All four run in CI and are required on `main`, along with the PR title check
and the Claude review.
