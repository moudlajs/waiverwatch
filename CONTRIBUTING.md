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
docker build -t waiverwatch . # the Cloud Run image; CI also checks /health
```

All five run in CI and are required on `main`, along with the PR title check
and the Claude review.

## Changing the review workflow

`claude-code-action` refuses to run from a PR that edits
`.github/workflows/claude-review.yml`; it only trusts the version on `main`.
The job then fails on purpose, so such a PR can't slip through unreviewed.
To merge one, the owner explicitly approves lifting `claude-review` from the
`main` ruleset's required checks, merges, and restores it straight away. The
next PR's review runs the new workflow.

## Deploying

Hosted on Google Cloud Run (`europe-west1`), project `waiverwatch-509716`.

- **One-time setup:** `deploy/setup.sh <project-id> <billing-account-id>`
  creates the Artifact Registry repository, a runtime service account with no
  project roles (it can read only the token signing key), a deploy service account that GitHub Actions reaches through
  Workload Identity Federation (only from `main` of this repository, no keys),
  a budget alert, the `GCP_*` repository variables, and the token signing
  key (`waiverwatch-signing-key`, random, generated once, never printed). Safe to re-run.
- **Runtime settings** (repository variables, not secrets):
  `WAIVERWATCH_BASE_URL` is the service URL the connector is added with, and
  `WAIVERWATCH_ALLOWED_USERS` (optional, comma-separated Sleeper usernames)
  restricts sign-in. To let anyone in, **delete** the variable (GitHub doesn't
  allow empty ones); redeploy for either change to apply (docs/multi-user.md).
  When `WAIVERWATCH_BASE_URL` is unset, deploys use the existing service's
  URL. From scratch there is no service yet, so create a placeholder once to
  get its URL, then set the variable before the first release:

  ```sh
  gcloud run deploy waiverwatch --region europe-west1 --no-allow-unauthenticated \
    --image us-docker.pkg.dev/cloudrun/container/hello
  gh variable set WAIVERWATCH_BASE_URL --body "$(gcloud run services describe \
    waiverwatch --region europe-west1 --format 'value(status.url)')"
  ```
- **Billing kill switch** (#51): Google Cloud has no hard spending cap, so
  the budget publishes to Pub/Sub and the private `waiverwatch-killswitch`
  service unlinks the project's billing once actual cost reaches the budget
  (25 CZK/month). Everything stops, including the connector. Cost data lags
  by hours, so it caps the damage rather than every cent. To recover, find
  the cause, then re-link billing:
  `gcloud billing projects link waiverwatch-509716 --billing-account <id>`.
  It runs the latest release's image and is (re)deployed by `setup.sh`;
  `KILLSWITCH_DRY_RUN=1 deploy/setup.sh …` deploys it log-only: an
  over-budget message then runs the real billing read and a permission
  check, everything except the unlink.
- **Every release:** merging the release-please PR tags the release, and
  `release.yml` calls `deploy.yml`: build, push, `gcloud run deploy`, then a
  smoke test that the new version is serving.
- **By hand / rollback:** Actions → Deploy → Run workflow on `main` with a
  tag (`gh workflow run deploy.yml -f tag=v0.4.0`). Deploys only work from
  `main`; the identity provider rejects other refs.
