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

Early. Today it's a CLI that lists your leagues. See the
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

## Run

```sh
go run .
```

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## Licence

[MIT](./LICENSE). Not affiliated with Sleeper.
