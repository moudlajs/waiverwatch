# waiverwatch

[![CI](https://github.com/moudlajs/waiverwatch/actions/workflows/ci.yml/badge.svg)](https://github.com/moudlajs/waiverwatch/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/moudlajs/waiverwatch)](https://github.com/moudlajs/waiverwatch/releases)

Ask Claude about all your [Sleeper](https://sleeper.com) fantasy football
leagues at once, from your laptop or your phone.

- *"How am I doing this week?"* across every league
- *"Is my lineup right, and who should I pick up?"*
- *"Is Gibbs for Ja'Marr Chase and a 2027 1st fair in my dynasty league?"*

The Sleeper app shows one league at a time; waiverwatch answers across all of
them, with live scores, projections and trade values.

## Add it to Claude

1. On **claude.ai in a browser**: **Customize → Connectors → Add custom
   connector**, name `waiverwatch`, URL:

   ```
   https://waiverwatch-44okyiteea-ew.a.run.app/mcp
   ```

2. Click **Add**, then **Connect**, and sign in with **your Sleeper
   username**. No password; it only reads public Sleeper data.
3. On your phone, switch it on per chat under **+ → Connectors**, and ask.

Gemini, troubleshooting and limits: [docs/connector.md](docs/connector.md).

## What you can ask

Game day (matchups, lineups, injuries), waivers (who to pick up, FAAB bids),
trades (values, fair or not, who to target), playoff chances and draft
history. Examples and every tool: [docs/questions.md](docs/questions.md).

## Good to know

Projections and trade values are estimates for standard, half or full PPR.
waiverwatch stores nothing about you and never logs usernames
([privacy](docs/connector.md#privacy)). It's a free hobby project, so it may
be slow or down sometimes. Not affiliated with Sleeper, FantasyCalc or
DynastyProcess.

## Run it yourself

Locally in Claude Code or Claude Desktop: [docs/local.md](docs/local.md).
Hosting your own: [CONTRIBUTING.md](CONTRIBUTING.md#deploying). How it's
built: [docs/how-it-works.md](docs/how-it-works.md).

## Development

See [CONTRIBUTING.md](CONTRIBUTING.md). Plan: the
[milestones](https://github.com/moudlajs/waiverwatch/milestones); changes: the
[changelog](CHANGELOG.md).

## License

[MIT](LICENSE)
