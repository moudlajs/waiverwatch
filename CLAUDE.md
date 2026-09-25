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
```

Target layout (grow into it, don't create empty packages):

```
main.go              wiring only
internal/sleeper/    API client: HTTP + JSON, nothing else
internal/league/     domain logic: knows football, not HTTP or MCP
internal/store/      Store interface + in-memory impl
internal/mcp/        tool definitions: knows MCP, never does HTTP
```

`sleeper` never imports `mcp`; `mcp` never does HTTP.

- **Refresh on demand.** Live data (rosters, matchups, trending, state) is
  fetched on every tool call. No TTL caches for live data: stale is wrong.
- **Player dictionary** (`/players/nfl`, ~15 MB) is the only cache. Sleeper
  asks for at most one fetch per day. It is the join table: every other
  endpoint returns player IDs only.
- **Storage** sits behind a `Store` interface; in-memory now. Cloud Run's
  disk is ephemeral, so nothing may depend on local files surviving.
- **Transports:** stdio for local Claude Code / Desktop; Streamable HTTP with
  a bearer token when hosted. Same tools behind both.
- **Tools are coarse.** One call returning a useful chunk beats several
  chatty ones. Work across all of the user's leagues by default.
- Fan out per-league requests concurrently (`errgroup`), with a
  `context.Context` and a client timeout on every request.

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
- Matchups carry actual points only (`points`, `starters_points`,
  `players_points`), no projections (checked 2026-09-25, see #17). There is
  no per-game status either, so "yet to play" can't be told from 0 points.

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
M4 Alerts. Versions come from commits via release-please.

## Out of scope

- A web UI. Claude is the UI.
- Writing to Sleeper (claims, trades, lineup changes). Read-only.
- Sports other than NFL. Keep the client sport-agnostic, build nothing for it.
- Other people's leagues for now, but don't make multi-user impossible.
