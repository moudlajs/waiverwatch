# Using the connector

## Claude

Works on claude.ai, the Claude desktop app and the Claude mobile app. You need
a Claude plan that allows custom connectors.

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

**After an update** with new tools, Claude may not see them in your chats
yet: it keeps its own copy of the tool list. Disconnect and reconnect
waiverwatch under Connectors, then start a new chat. In an old chat, Claude
should notice a missing tool and tell you to do this.

## Gemini

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

## Numbers are estimates

Projections and trade values use standard, half or full PPR; custom scoring
like TE premium or 6-point passing touchdowns isn't applied. Projections are
for the whole game, not what's left of it. Trade values come from
[FantasyCalc](https://fantasycalc.com); if it's down, they come from
[DynastyProcess](https://github.com/dynastyprocess/data)'s open dynasty
values instead, and the answer says so.

## Privacy

There is no password: waiverwatch only reads public Sleeper data for the
username you enter, the same data anyone can see in the Sleeper app.
waiverwatch stores nothing about you. Trade values are fetched per league
format (dynasty or not, QB count, size, PPR), never with your username or
league. To count usage it logs, per request, which tool ran and whether it
worked, with an anonymous ID that changes every day (it can't be turned back
into your username, and days can't be linked). Google's standard request logs
keep IP addresses, paths and status codes. Logs are kept 30 days; usernames
are never logged. Design: [multi-user.md](multi-user.md).

## Limits

30 requests a minute per person, and a shared budget toward Sleeper. If
Claude reports "slow down" or "call budget", wait a minute. It's a free hobby
project: it may be slow or down sometimes.
