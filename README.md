# waiverwatch

An [MCP](https://modelcontextprotocol.io) server for [Sleeper](https://sleeper.com)
fantasy football. Ask Claude about all your leagues at once, from your laptop or
your phone:

- *"How am I doing this week?"* across every league
- *"Who's thin at RB in my dynasty league?"*
- *"Which trending waiver pickups are still available in my leagues?"*

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

Then just ask:

- *"How are my matchups this week?"*
- *"Who's hurt on my teams, and who can I pick up instead?"*
- *"Who should I pick up at RB?"* or *"…in my dynasty league?"*
- *"What's trending on waivers, and where is he still available?"*
- *"Compare me with my opponent in my 12-team league."*
- *"Where am I thin? Any position without a backup?"*
- *"Where did I draft Kenneth Walker?"*

**Privacy.** There is no password: waiverwatch only reads public Sleeper
data for the username you enter, the same data anyone can see in the
Sleeper app. waiverwatch stores nothing. Google's standard request logs
keep IP addresses, paths and status codes for 30 days; usernames are not
logged.

**Limits.** 30 requests a minute per person, and a shared budget toward
Sleeper. If Claude reports "slow down" or "call budget", wait a minute.
It's a free hobby project: it may be slow or down sometimes.

## Status

Tools: `list_leagues`, `get_matchups`, `get_roster`, `compare_rosters`,
`draft_results`, `injury_report`, `position_depth`, `trending_players`,
`waiver_targets`. See the
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

| Tool | Returns |
|---|---|
| `list_leagues` | every league this season: type, size, your team, record, points, standing |
| `get_matchups` | this week (or any week) in every league: live score, your and your opponent's starters with injuries; guillotine rank and margin over last place |
| `get_roster` | a team's starters (by slot), bench, IR and taxi with injuries: yours, or any owner by name |
| `compare_rosters` | your roster next to this week's opponent (or any owner), position by position, with records |
| `draft_results` | your picks in every league (dynasty: startup and rookie drafts too); "where did I draft X?" |
| `position_depth` | per league: starting slots (flex-aware) vs healthy players and backups; every thin spot across leagues |
| `injury_report` | your injured players across all leagues, where they start for you, and a free replacement in each of those leagues |
| `trending_players` | the most-added players on Sleeper, with the leagues where you can still claim each one |
| `waiver_targets` | per league: the best free agents you can actually start there, your waiver priority or FAAB left |

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## Licence

[MIT](./LICENSE). Not affiliated with Sleeper.
