```text
             ⣿⡇                  ⣿⡇
         ⣠⣤⣤⣄⣿⡇  ⢀⣤⣤⣤⡀    ⣠⣤⣤⣄   ⣿⡇  ⢀⣤⡀
 ⣠⣦⡀   ⢠⣾⠟⠉⠉⠻⣿⡇ ⣴⡿⠋⠉⠙⢿⣦ ⢠⣾⠟⠉⠉⠉⠁  ⣿⡇⣠⣴⡿⠛⠁
⠺⣿⣿⡿⠂  ⢸⣿⡀  ⢀⣿⡇ ⣿⣿⠿⠿⠿⠿⠿ ⢸⣿⡀      ⣿⣿⠟⢿⣷⡀
 ⠈⠋     ⠙⢿⣶⣶⡿⣿⡇ ⠈⠻⣷⣶⣶⠖   ⠙⢿⣶⣶⡶⠂  ⣿⡇  ⠙⢿⡆
```

**one deck for all your agents**

Deck is a terminal app for running Claude Code, Codex and OpenCode side by side in one chat. It drives the CLIs you already have, under your own logins: a session holds you and any number of agents, they can hand work to each other where you can see it, and every reply, tool call, diff and approval shows up in one place.

Deck never calls a model API and never reads a credential. It starts no language servers or MCP servers of its own, sends no telemetry and checks for no updates.

## Install

macOS and Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/dev-dami/deck/main/install.sh | sh
```

Windows (PowerShell):

```powershell
powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/dev-dami/deck/main/install.ps1 | iex"
```

Both install the latest release (checked against its checksum): to `~/.local/bin` on macOS and Linux, and to `%LOCALAPPDATA%\deckin` on Windows, which is added to your PATH. `DECK_VERSION=v0.7.0` picks a release and `DECK_INSTALL_DIR` another folder. With Go: `go install github.com/dev-dami/deck@latest`.

Sign in to `claude`, `codex` and/or `opencode` yourself, then check what Deck sees:

```sh
deck doctor
```

## Use

Run `deck`, type, press enter.

- **Who answers.** Type `$` to pick an agent and the model it should run: `$claude`, `$codex`, `$opencode` bring that agent in, `$all` asks everyone; otherwise whoever spoke last answers. `$` names that are not agents (`$HOME`) stay plain text.
- **Files.** Type `@` to search the folder's files and add one to the message. Each agent speaks under its own CLI's mark and colour: `✻ claude`, `>_ codex`, `▯ opencode`.
- **Agents talk to each other.** An agent can hand work to another; every hand-off shows in the chat as `→ $codex …`. After six agent-to-agent hand-offs Deck pauses until you write.
- **Working.** While agents work, the bottom line says who and for how long, with a spinner in each agent's colour.
- **Models and effort.** `$` switches an agent's model as you address it; `ctrl+l` lists every model your CLIs offer (OpenCode: every model of every provider it is set up for), then the reasoning efforts that model takes.
- **Approvals** open as a dialog with the command or the diff: allow, allow for the session, or deny.
- **Plan and yolo.** `tab` switches plan mode, where agents read but change nothing. `ctrl+y` is yolo: everything is approved, and the screen tells you so.
- **Pastes** of any size go in whole; big ones become an attachment the agent still reads in full.
- **Sessions.** `ctrl+n` starts one, `ctrl+s` switches. `deck -C` continues the last one.
- `ctrl+p` lists every command; `esc` twice stops the agents.

| mode | Claude Code | Codex | OpenCode |
| --- | --- | --- | --- |
| plan | `--permission-mode plan` | read-only sandbox | its plan agent |
| ask | asks you through Deck | `on-request`, `workspace-write` | edits, commands and fetches ask you |
| yolo | `bypassPermissions` | `never`, `danger-full-access` | everything approved |

## Config

`~/.deck/config.json`, all keys optional:

```json
{
  "user_name": "",
  "default_agent": "claude",
  "motion": "on",
  "bell": true,
  "tell_agents_name": false,
  "track_changes": true,
  "claude_binary": "claude",
  "codex_binary": "codex",
  "opencode_binary": "opencode",
  "models": { "claude": "", "codex": "", "opencode": "" },
  "efforts": { "claude": "", "codex": "", "opencode": "" }
}
```

`DECK_REDUCED_MOTION=1` turns animations off.

Agents are drawn with their own CLI's mark: Claude Code's `✻`, Codex CLI's `>_` and OpenCode's `▯`. `deck doctor` shows the real logos in terminals that can draw images (Kitty, Ghostty, Windows Terminal, iTerm2, WezTerm). `DECK_IMAGES=off` turns them off.

The interface's own settings (theme, notifications, mouse) live in `~/.config/deck/deck.json` and the project's `.deck/deck.json`; the `Themes` command switches between Deck's dark and light themes.

## How it works

Deck never calls a model API and never touches your credentials. It starts your CLIs as child processes and reads their structured output: `claude -p` with stream-json, `codex app-server` over JSON-RPC, and `opencode acp` over the Agent Client Protocol. For OpenCode, Deck sets one thing, in the child's environment only (`OPENCODE_CONFIG_CONTENT`, laid over any you already set): edits, commands and fetches ask first, so Deck's approvals and modes decide them. Your OpenCode config files are never written. Approvals and agent messages travel through a small MCP helper (`deck mcp-approve`) to Deck over loopback, authenticated with a per-run token. Usage counts against your own Claude and Codex plans and whatever providers OpenCode uses.

In a git work tree, each message you send records the tree as a git snapshot (tracked edits and untracked files that are not ignored). The snapshot is written through a throwaway index, so your branch, index, stash and refs are never touched. Its objects sit unreferenced in `.git` until `git gc` prunes them. Undo writes files the way `git checkout` would. Set `track_changes` to `false` to turn this off. Outside git the changes panel shows what the agents reported, without undo.

Deck is not affiliated with Anthropic or OpenAI.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports: [SECURITY.md](SECURITY.md).

## License

Deck's own code is MIT ([LICENSE](LICENSE)). Its interface is derived from Crush, used under the Functional Source License 1.1 with an MIT future license ([LICENSE.crush.md](LICENSE.crush.md)).
