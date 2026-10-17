# How it works

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

Sign-in for the hosted connector: [multi-user.md](multi-user.md).
