```text
             ⣿⡇                  ⣿⡇
         ⣠⣤⣤⣄⣿⡇  ⢀⣤⣤⣤⡀    ⣠⣤⣤⣄   ⣿⡇  ⢀⣤⡀
 ⣠⣦⡀   ⢠⣾⠟⠉⠉⠻⣿⡇ ⣴⡿⠋⠉⠙⢿⣦ ⢠⣾⠟⠉⠉⠉⠁  ⣿⡇⣠⣴⡿⠛⠁
⠺⣿⣿⡿⠂  ⢸⣿⡀  ⢀⣿⡇ ⣿⣿⠿⠿⠿⠿⠿ ⢸⣿⡀      ⣿⣿⠟⢿⣷⡀
 ⠈⠋     ⠙⢿⣶⣶⡿⣿⡇ ⠈⠻⣷⣶⣶⠖   ⠙⢿⣶⣶⡶⠂  ⣿⡇  ⠙⢿⡆
```

**one deck for all your agents**

[![CI](https://github.com/dev-dami/deck/actions/workflows/ci.yml/badge.svg)](https://github.com/dev-dami/deck/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

![deck demo](docs/demo.gif)

Deck is a terminal app for running Claude Code and Codex side by side. It sits on top of the CLIs you already have: a thread holds you and any number of agents, they can talk to each other where you can see it, and every tool call, diff and approval shows up in one place.

Inspired by T3 Code, built for the terminal. Early: expect rough edges.

## Install

```sh
go install github.com/dev-dami/deck/cmd/deck@latest
```

Or grab a binary from [Releases](https://github.com/dev-dami/deck/releases).

Deck drives your own `claude` and `codex` CLIs. Sign in to them yourself, then check what Deck sees:

```sh
deck doctor
```

## Use

Run `deck`, type, press enter.

- **Threads, not agents.** A thread holds you and one or more agents. `@codex review this` talks to one, `@claude plan it, @codex review it` to both, `@all` to everyone. Mentioning an agent the thread lacks adds it.
- **Agents talk to each other** through a `send_message` tool or by starting a line with `@name`. Every relay shows in the transcript. After six agent-to-agent messages the thread pauses until you continue or steer.
- **Permission modes.** `shift+tab` or a click on the chip cycles `◇ plan` → `● ask` → `▲ yolo`. Yolo is never the startup default.
- **Models.** Click the model in the chip line or press `alt+m` to pick from the models the CLI offers.
- **Approvals** appear inline and in the inbox (`i`): `y` allow, `a` always, `n` deny, `e` edit.
- **Images.** `ctrl+v` pastes one from the clipboard; pasting or dropping an image path attaches it.
- **Queue and steer.** Messages sent while an agent works wait above the composer; click one to steer the agent with it now.
- **Edit.** `↑` on an empty composer, or a click on your message, edits it and resends it marked as edited.
- **Context.** Each agent's context window use shows in the chip line.

| mode | Claude Code | Codex |
| --- | --- | --- |
| `◇ plan` | `--permission-mode plan` | read-only sandbox |
| `● ask` | prompts come to Deck's inbox | `on-request`, `workspace-write` |
| `▲ yolo` | `--permission-mode bypassPermissions` | `never`, `danger-full-access` |

Commands: `/new`, `/resume`, `/model`, `/cd <path>`, `/name <name>`. `ctrl+c` interrupts; twice quits.

## Config

`~/.deck/config.json`, all keys optional:

```json
{
  "user_name": "",
  "default_agent": "claude",
  "motion": "on",
  "bell": true,
  "tell_agents_name": false,
  "claude_binary": "claude",
  "codex_binary": "codex",
  "models": { "claude": "", "codex": "" }
}
```

`DECK_REDUCED_MOTION=1` turns animations off.

## How it works

Deck never calls a model API and never touches your credentials. It starts your CLIs as child processes and reads their structured output: `claude -p` with stream-json, and `codex app-server` over JSON-RPC. Approvals and agent messages travel through a small MCP helper (`deck mcp-approve`) to Deck over loopback, authenticated with a per-run token. Usage counts against your own Claude and Codex plans.

Deck is not affiliated with Anthropic or OpenAI.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports: [SECURITY.md](SECURITY.md).

## License

MIT
