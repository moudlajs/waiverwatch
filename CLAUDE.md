# Notes for Claude

Read `CONTRIBUTING.md` first. Branch naming, PR flow and title format apply
to you.

## The rule

**One issue = one branch = one draft PR.** Open as draft, link the issue with
`Closes #N`, mark ready only when CI is green. Never push to `main`.

Never write the literal `@claude` in a GitHub comment, PR, or issue body: it
triggers a Claude run on the owner's subscription.

## What this is

waiverwatch: a Go MCP server for Sleeper fantasy football. The owner plays
in many leagues at once (10 to 32 teams; dynasty, redraft, survival, cash)
and asks Claude about all of them, mostly from a phone. Speed of answers on
waiver day matters more than polish.

## Architecture

```
Claude (phone/web/desktop) --MCP--> waiverwatch (Cloud Run) --HTTP--> api.sleeper.app
                                                               --HTTP--> api.fantasycalc.com (trade values)
```

Target layout (grow into it, don't create empty packages):

```
main.go              wiring only
internal/sleeper/    API client: HTTP + JSON, nothing else
internal/fantasycalc/ trade values client: HTTP + JSON, nothing else
internal/dynastyprocess/ backup trade values: HTTP + CSV, nothing else
internal/league/     domain logic: knows football, not HTTP or MCP
internal/store/      Store interface + in-memory impl
internal/mcp/        tools + transport: knows MCP, never calls Sleeper
internal/auth/       OAuth sign-in for the hosted server: knows OAuth only
internal/killswitch/ unlinks billing when the budget is spent (own Cloud Run service)
cmd/killswitch/      its binary, shipped in the same image
```

`sleeper` never imports `mcp`; `mcp` never calls Sleeper (serving HTTP is
fine, it's the transport).

- **Fresh within a minute.** `sleeper.Client` keeps raw responses in a
  shared in-memory cache (lost on scale-to-zero, fine): rosters, members and
  matchups 60 s; NFL state, trending and projections 5 min; username lookups, a user's
  leagues and draft lists 10 min; earlier seasons' leagues and completed
  drafts' picks 24 h. Shared across users, so league mates share fetches;
  concurrent misses share one request (singleflight); errors are never
  cached; hits don't spend the call budget. Never longer than a minute for
  anything that changes during games.
- **Player dictionary** (`/players/nfl`, ~15 MB) is cached decoded for a day
  by `league.Directory`, not by the client. Sleeper asks for at most one
  fetch per day. It is the join table: every other endpoint returns player
  IDs only.
- **Errors are for people.** Sleeper failures become `ErrRateLimited` (429),
  `ErrUnavailable` (5xx, unreachable) and our own `ErrBusy` (call budget),
  worded so Claude can pass them on as they are.
- **Storage** sits behind a `Store` interface; in-memory now. Cloud Run's
  disk is ephemeral, so nothing may depend on local files surviving.
- **Transports:** stdio for local Claude Code / Desktop; stateless
  Streamable HTTP at `/mcp` when `PORT` is set (Cloud Run), plus `/health`
  (never `/healthz`: Cloud Run reserves paths ending in `z`).
  Same tools behind both.
- **Hosted sign-in (`internal/auth`):** waiverwatch is its own tiny OAuth 2.1
  authorization server. Claude identifies itself with a Client ID Metadata
  Document; only Claude's two client IDs are accepted (claude.ai and Claude
  Code). Other clients (Gemini app and CLI) use Dynamic Client Registration
  (`POST /register`, #105): stateless, the client_id is a signed token
  holding the redirect URIs; only loopback http or HTTPS on `*.google.com`
  may be registered, so tokens can only go back to the user's machine or
  Google. People sign in with their **Sleeper username** (checked against
  Sleeper; public data, so no password: docs/multi-user.md); tokens carry the
  Sleeper user id and username, and every tool answers for the token's user
  (`auth.UserFrom` → `league.WithUser`). stdio uses `WAIVERWATCH_USER`.
  Tokens are HMAC-signed and stateless (1h access, 90d refresh), so restarts
  don't sign anyone out. A refresh re-checks the optional allowlist
  (`WAIVERWATCH_ALLOWED_USERS`) but can't revoke old tokens (nothing is
  stored); rotating the signing key signs everyone out. HTTP mode refuses to
  start without `WAIVERWATCH_BASE_URL` and `WAIVERWATCH_SIGNING_KEY`;
  `WAIVERWATCH_NO_AUTH=1` (with `WAIVERWATCH_USER`) is for local testing only.
- **Tools are coarse.** One call returning a useful chunk beats several
  chatty ones. Work across all of the user's leagues by default.
- Fan out per-league requests concurrently (`errgroup`), with a
  `context.Context` and a client timeout on every request.
- **Limits** (#56): `sleeper.Client` holds every call to 10/s (600/min,
  under Sleeper's 1000/min per IP), waiting up to 10s, else `ErrBusy`. Each
  signed-in user gets 30 tool calls a minute (burst 10) via the `gate` that
  wraps every tool (`mcp.limited`); stdio is unlimited. Both are per
  instance: raising `max-instances` multiplies the Sleeper budget.

## Sleeper API

Public, no key, undocumented and unversioned. Base `https://api.sleeper.app/v1`.

```
GET /user/{username}                       -> user_id
GET /user/{user_id}/leagues/nfl/{season}   -> leagues
GET /league/{league_id}/rosters            -> rosters (player IDs only)
GET /league/{league_id}/users              -> owners' display names
GET /league/{league_id}/matchups/{week}    -> live scores
GET /state/nfl                             -> current season and week
GET /players/nfl                           -> player dictionary (~15 MB)
GET /players/nfl/trending/add              -> most-added players
GET /league/{league_id}                    -> one league (previous_league_id)
GET /league/{league_id}/drafts             -> drafts
GET /draft/{draft_id}/picks                -> picks (picked_by, round, pick_no)
GET /league/{league_id}/traded_picks       -> picks that changed hands
GET /projections/nfl/{season_type}/{season}/{week} -> projected stats by player
```

- Starters fill `league.roster_positions` in order; `BN` slots follow.
- Roster to owner: `rosters[].owner_id` -> `users[].user_id` -> `display_name`.
- Never hardcode league size (10 to 32) or season; take the season and week
  from `/state/nfl`.
- **Tests never hit the real API.** `internal/sleeper` tests decode real
  captured responses in `testdata/` (the wire-format contract). Logic tests
  above it serve typed values through `sleeper/sleepertest`, so each case
  shows only what matters.
- Guillotine leagues (`settings.type` 3) have no head-to-head: each team has
  its own `matchup_id`, and eliminated teams keep a roster with no players.
- An empty lineup slot is the starter ID `"0"`.
- `/players/nfl/trending/add` returns at most 100 players whatever `limit`
  says. A roster's `players` already includes its taxi and IR players.
- `settings.waiver_type`: 0 rolling priority, 1 reverse standings, 2 FAAB
  (`waiver_budget`, minus the roster's `waiver_budget_used`). Players carry
  `search_rank` (1 is best; missing for ~2% of players).
- A dynasty league's startup draft lives in an earlier season's league:
  follow `previous_league_id`. Redraft leagues' previous seasons are other
  drafts entirely and aren't followed. `picked_by` is the user who picked;
  `roster_id` only identifies a team within one season's league.
- `traded_picks` lists only traded picks, past drafts included: `roster_id`
  is the pick's original team, `owner_id` the roster holding it now. Every
  other pick belongs to its original team; `settings.draft_rounds` gives the
  rounds.
- Matchups carry actual points only (`points`, `starters_points`,
  `players_points`). Projections come from `/projections/nfl/...` (#17;
  ~600 KB, player ID → stats incl. `pts_std`, `pts_half_ppr`, `pts_ppr`;
  players on bye have no `pts_*`). We pick by `scoring_settings.rec`; custom
  bonuses aren't applied. They are whole-game, not remaining: there is no
  per-game status, so "yet to play" can't be told from 0 points.

## FantasyCalc API

Trade values, public, no key, undocumented. One endpoint:
`GET https://api.fantasycalc.com/values/current?isDynasty=&numQbs=&numTeams=&ppr=`.

- Each entry has `player.sleeperId`, so it joins straight onto Sleeper IDs.
  Dynasty markets add draft picks (`position: PICK`, IDs like
  `FP_2027_early_0`).
- Markets (checked 2026-10-06): `numQbs` 1 or 2, `numTeams` 8/10/12/14
  (16+ silently returns the 12-team values), `ppr` 0/0.5/1; anything else 404s.
  `fantasycalc.Settings.Normalise` maps leagues onto these (bigger leagues:
  14). `league.ValueSettings` derives them: dynasty only for `settings.type`
  2; 2 QBs when `QB` + `SUPER_FLEX` slots ≥ 2; PPR from `scoring_settings.rec`.
- ~150 KB redraft, ~330 KB dynasty. Cached per market for 3 h; on a failed
  refresh the stale copy is served.
- **Backup:** when FantasyCalc fails, `Service.market` falls back to
  DynastyProcess (`github.com/dynastyprocess/data`: `values-players.csv`
  joined to Sleeper via `db_playerids.csv` on `fp_id` = `fantasypros_id`;
  missing IDs are `NA`). Dynasty values only, 1QB or superflex, no picks; the
  answer says so in a note. Their repo is GPL-3, so test fixtures are made
  up in their format, never copied.

## Go conventions

- Errors are values, wrapped with context: `fmt.Errorf("fetching rosters: %w", err)`.
  No `panic` outside `main`.
- Table-driven tests with `t.Run`. Test our logic (joins, error paths), not
  `encoding/json`.
- Names: `sleeper.Client`, never `sleeper.SleeperClient`.
- KISS: no abstractions, dependencies or config ahead of need. The MCP Go SDK
  and `errgroup` are expected dependencies; ask before adding others.

## Milestones

M0 Foundation · M1 Local MCP · M2 Hosted (phone) · M3 Waiver insights ·
M4 Alerts · M5 Multi-user · M6 Trade values. Versions come from commits via
release-please.

## Out of scope

- A web UI. Claude is the UI.
- Writing to Sleeper (claims, trades, lineup changes). Read-only.
- Sports other than NFL. Keep the client sport-agnostic, build nothing for it.
- Other people's leagues for now, but don't make multi-user impossible.
