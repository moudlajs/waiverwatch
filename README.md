# waiverwatch

An [MCP](https://modelcontextprotocol.io) server for [Sleeper](https://sleeper.com)
fantasy football. Ask Claude about all your leagues at once, from your laptop or
your phone:

- *"How am I doing this week?"* across every league
- *"Who's thin at RB in my dynasty league?"*
- *"Which trending waiver pickups are still available in my leagues?"*

The Sleeper app shows one league at a time. Fantasy needs reaction: an injury
on Sunday gives you minutes to hit the waiver wire, wherever you are.

## Status

Runs locally over stdio, and hosted on Cloud Run for claude.ai and the
Claude mobile app. Tools so far: `list_leagues`, `get_matchups`, `get_roster`, `compare_rosters`, `draft_results`, `injury_report`, `trending_players`, `waiver_targets`. See the
[milestones](https://github.com/moudlajs/waiverwatch/milestones) for the plan.

## Principles

- **Always fresh.** Live data (rosters, matchups, waivers) is fetched from
  Sleeper on every request. Only the player dictionary is cached.
- **Read-only.** Public Sleeper API, no account credentials, no scraping.
- **Claude is the UI.** No web frontend.
- **Zero running cost.** Free tiers only.

## Stack

| Piece | Choice |
|---|---|
| Language | Go |
| Interface | MCP ([Go SDK](https://github.com/modelcontextprotocol/go-sdk)): stdio locally, Streamable HTTP when hosted |
| Host | Google Cloud Run (free tier, scales to zero) |
| Data | `api.sleeper.app`: public, free, no key |

## Use it locally

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

## Use it on claude.ai and your phone

1. On **claude.ai in a browser** (connectors can't be added from the phone
   app): **Customize → Connectors → Add custom connector**.
2. URL: `https://waiverwatch-44okyiteea-ew.a.run.app/mcp`. Keep the detected
   settings ("Sign in now", "Use Claude's published identity") and click
   **Add**, then **Connect**.
3. On the waiverwatch sign-in page, enter **your Sleeper username**. No
   password: waiverwatch only reads public Sleeper data, and stores nothing.
4. The connector syncs to the Claude mobile app. Enable it per chat with
   **+ → Connectors**.

To host your own, see [CONTRIBUTING.md](./CONTRIBUTING.md#deploying).

| Tool | Returns |
|---|---|
| `list_leagues` | every league this season: type, size, your team, record, points, standing |
| `get_matchups` | this week (or any week) in every league: live score, your and your opponent's starters with injuries; guillotine rank and margin over last place |
| `get_roster` | a team's starters (by slot), bench, IR and taxi with injuries: yours, or any owner by name |
| `compare_rosters` | your roster next to this week's opponent (or any owner), position by position, with records |
| `draft_results` | your picks in every league (dynasty: startup and rookie drafts too); "where did I draft X?" |
| `injury_report` | your injured players across all leagues, where they start for you, and a free replacement in each of those leagues |
| `trending_players` | the most-added players on Sleeper, with the leagues where you can still claim each one |
| `waiver_targets` | per league: the best free agents you can actually start there, your waiver priority or FAAB left |

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## Licence

[MIT](./LICENSE). Not affiliated with Sleeper.
