# Running it locally

For developers, or to use it in Claude Code or Claude Desktop without the
hosted connector. Locally there's no sign-in: it answers for the Sleeper user
in `WAIVERWATCH_USER`.

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

To host your own, see [CONTRIBUTING.md](../CONTRIBUTING.md#deploying).
