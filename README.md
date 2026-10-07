# waiverwatch

An [MCP](https://modelcontextprotocol.io) server for [Sleeper](https://sleeper.com)
fantasy football. Ask Claude about all your leagues at once, from your laptop or
your phone:

- *"How am I doing this week?"* across every league
- *"Which trending waiver pickups are still available in my leagues?"*
- *"Is Gibbs for Ja'Marr Chase and a 2027 1st fair in my dynasty league?"*

The Sleeper app shows one league at a time. Fantasy needs reaction: an injury
on Sunday gives you minutes to hit the waiver wire, wherever you are.

## Add it to your Claude (for Sleeper players)

Works on claude.ai, the Claude desktop app and the Claude mobile app. You
need a Claude plan that allows custom connectors.

1. On **claude.ai in a browser** (connectors can't be added from the phone
   app): **Customize → Connectors → Add custom connector**.
2. Name `waiverwatch`, URL:

   ```
   https://waiverwatch-44okyiteea-ew.a.run.app/mcp
   ```

   Keep the detected settings ("Sign in now", "Use Claude's published
   identity") and click **Add**, then **Connect**.
3. On the waiverwatch sign-in page, type **your Sleeper username** and click
   **Sign in**. It then syncs to the Claude app on your phone; switch it on
   per chat under **+ → Connectors**.

### Gemini

Gemini can use waiverwatch too; you sign in the same way, with your Sleeper
username.

- **Gemini CLI** (free, anywhere): add to `~/.gemini/settings.json`, then
  run `/mcp auth waiverwatch` in Gemini CLI to sign in:

  ```json
  {
    "mcpServers": {
      "waiverwatch": { "httpUrl": "https://waiverwatch-44okyiteea-ew.a.run.app/mcp" }
    }
  }
  ```
- **Gemini app:** Google limits custom apps to personal accounts in the US,
  in English, on paid Google AI plans. If you qualify: on
  [gemini.google.com](https://gemini.google.com), **Settings → Connected
  apps → Add a custom app**, with the same URL. This path hasn't been tried
  yet; tell us if it works.

## What you can ask

Every question covers all your leagues unless you name one ("…in my dynasty
league"); Claude matches league names loosely.

**Game day**

- *"How are my matchups this week?"* Live and projected scores, starter by
  starter, for you and your opponent. Survival (guillotine) leagues show your
  rank and how far you are above last place.
- *"Is my lineup right in every league?"* Empty slots, starters who are out or
  on bye, and bench players projected to beat a starter, with the changes to
  make and the points they're worth.
- *"Who's hurt on my teams, and who can I pick up instead?"*
- *"Compare me with my opponent in my 12-team league."*

**Waivers**

- *"Who should I pick up at RB, and how much should I bid?"* The best free
  agents you can actually start in each league, ranked by rest-of-season
  value and this week's projection (survival leagues: this week first), with
  your waiver priority, or your FAAB left and a suggested bid.
- *"What's trending on waivers, and where is he still available?"*
- *"Where am I thin? Any position without a backup?"*

**Trades** (values from [FantasyCalc](https://fantasycalc.com), built from real
fantasy trades and fitted to each league: dynasty or redraft, superflex or
1QB, league size, PPR)

- *"What's my roster worth in my dynasty league, and where do I rank?"*
- *"Is Gibbs for Chase and a 2027 1st fair?"* Both sides valued, adjusted so
  two good players don't automatically beat one great one, with who it leans
  to and your depth before and after. Draft picks are checked against who
  actually holds them; next year's picks are valued early, mid or late from
  the original team's record.
- *"Who should I trade for?"* Your thin positions, the spare players you can
  afford to give up, and players on other teams they can buy. Deals that also
  fill the other team's thin spot come first: those get accepted.

**History**

- *"Where did I draft Kenneth Walker?"* Dynasty leagues include the startup
  draft and past rookie drafts.

The [tools](#tools) section lists exactly what each one returns.

## Good to know

**After an update** with new tools, Claude may not see them in your chats
yet: it keeps its own copy of the tool list. Disconnect and reconnect
waiverwatch under Connectors, then start a new chat. In an old chat, Claude
should notice a missing tool and tell you to do this.

**Numbers are estimates.** Projections and trade values use standard, half
or full PPR; custom scoring like TE premium or 6-point passing touchdowns
isn't applied. Projections are for the whole game, not what's left of it.
If FantasyCalc is down, values come from
[DynastyProcess](https://github.com/dynastyprocess/data)'s open dynasty
values instead, and the answer says so.

**Privacy.** There is no password: waiverwatch only reads public Sleeper
data for the username you enter, the same data anyone can see in the
Sleeper app. waiverwatch stores nothing about you. Trade values are fetched
per league format (dynasty or not, QB count, size, PPR), never with your
username or league. To count usage it logs, per request, which tool ran and
whether it worked, with an anonymous ID that changes every day (it can't be
turned back into your username, and days can't be linked). Google's standard
request logs keep IP addresses, paths and status codes. Logs are kept 30
days; usernames are never logged.

**Limits.** 30 requests a minute per person, and a shared budget toward
Sleeper. If Claude reports "slow down" or "call budget", wait a minute.
It's a free hobby project: it may be slow or down sometimes.

## Status

Built and run by one person; see the
[milestones](https://github.com/moudlajs/waiverwatch/milestones) for the plan
and the [changelog](./CHANGELOG.md) for what changed.

## Principles

- **Fresh within a minute.** Rosters, matchups and waivers are at most a
  minute old; the player list is refreshed daily. A short shared cache keeps
  Sleeper's load low when many people ask at once.
- **Read-only.** Public Sleeper API, no account credentials, no scraping.
- **Claude is the UI.** No web frontend.
- **Zero running cost.** Free tiers only.

## Stack

| Piece | Choice |
|---|---|
| Language | Go |
| Interface | MCP ([Go SDK](https://github.com/modelcontextprotocol/go-sdk)): stdio locally, Streamable HTTP when hosted |
| Host | Google Cloud Run (free tier, scales to zero) |
| Data | `api.sleeper.app`: public, free, no key; trade values from `api.fantasycalc.com`, same; DynastyProcess's open values as a backup |

## Run it locally (developers)

Install (needs Go; puts `waiverwatch` in `$(go env GOPATH)/bin`):

```sh
go install github.com/moudlajs/waiverwatch@latest
```

**Claude Code:**

```sh
claude mcp add --scope user waiverwatch -e WAIVERWATCH_USER=<your Sleeper username> -- "$(go env GOPATH)/bin/waiverwatch"
```

**Claude Desktop:** Settings → Developer → Edit Config, then add to
`claude_desktop_config.json` (use the absolute path; Desktop doesn't read
your shell's `PATH`):

```json
{
  "mcpServers": {
    "waiverwatch": {
      "command": "/Users/<you>/go/bin/waiverwatch",
      "env": { "WAIVERWATCH_USER": "<your Sleeper username>" }
    }
  }
}
```

Restart Claude and ask *"How are my fantasy leagues looking?"*

## Host your own

To host your own, see [CONTRIBUTING.md](./CONTRIBUTING.md#deploying).

## Tools

| Tool | Returns |
|---|---|
| `list_leagues` | every league this season: type, size, your team, record, points, standing |
| `get_matchups` | this week (or any week) in every league: live and projected score, your and your opponent's starters with injuries and projections; guillotine rank and margin over last place |
| `lineup_check` | per league this week: empty slots, starters out or on bye, questionable starters, and the best lineup by projections (flex-aware) as start/bench changes |
| `get_roster` | a team's starters (by slot), bench, IR and taxi with injuries: yours, or any owner by name |
| `compare_rosters` | your roster next to this week's opponent (or any owner), position by position, with records and trade values (totals, starters, per position) |
| `draft_results` | your picks in every league (dynasty: startup and rookie drafts too); "where did I draft X?" |
| `position_depth` | per league: starting slots (flex-aware) vs healthy players and backups; every thin spot across leagues |
| `injury_report` | your injured players across all leagues, where they start for you, and a free replacement in each of those leagues |
| `player_values` | trade values (FantasyCalc, from real trades) fitted to each league's format: named players and who has them, or a whole roster with its total and the league's team value ranking |
| `evaluate_trade` | "is this trade fair?": both sides valued for that league (players, and dynasty picks checked against who holds them), 2-for-1 adjusted, who it leans to and by how much, your depth before and after |
| `trade_targets` | per league: your thin positions, the spare players you can afford to trade, and players on other teams they can buy, each with the cheapest offer |
| `trending_players` | the most-added players on Sleeper, with the leagues where you can still claim each one |
| `waiver_targets` | per league: the best free agents you can actually start there, ranked by trade value and projection, your waiver priority or FAAB left, and a suggested FAAB bid |

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## Licence

[MIT](./LICENSE). Not affiliated with Sleeper, FantasyCalc or DynastyProcess.
