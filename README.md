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

Early: runs locally over stdio. Tools so far: `list_leagues`, `get_matchups`. See the
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

| Tool | Returns |
|---|---|
| `list_leagues` | every league this season: type, size, your team, record, points, standing |
| `get_matchups` | this week (or any week) in every league: live score, your and your opponent's starters with injuries; guillotine rank and margin over last place |

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## Licence

[MIT](./LICENSE). Not affiliated with Sleeper.
