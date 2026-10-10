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

Deck is a terminal app for running Claude Code, Codex and OpenCode side by side. It sits on top of the CLIs you already have: a thread holds you and any number of agents, they can talk to each other where you can see it, and every tool call, diff and approval shows up in one place.

Inspired by T3 Code, built for the terminal. Early: expect rough edges.

## Install

```sh
go install github.com/dev-dami/deck/cmd/deck@latest
```

Or grab a binary from [Releases](https://github.com/dev-dami/deck/releases).

Deck drives your own `claude`, `codex` and `opencode` CLIs (any of them; OpenCode is optional). Sign in to them yourself, then check what Deck sees:

```sh
deck doctor
```

## Use

Run `deck`, type, press enter.

- **Threads, not agents.** A thread holds you and one or more agents. `@codex review this` talks to one, `@claude plan it, @codex review it` to both, `@all` to everyone. Mentioning an agent the thread lacks adds it.
- **Agents talk to each other** through a `send_message` tool or by starting a line with `@name`. Every relay shows in the transcript. After six agent-to-agent messages the thread pauses until you continue or steer.
- **Permission modes.** `shift+tab` or a click on the chip cycles `◇ plan` → `● ask` → `▲ yolo`. Yolo is never the startup default.
- **Models and effort.** Click the model in the chip line or press `alt+m` to pick from the models the CLI itself offers (the same list as Claude Code's `/model` and Codex's model picker; for OpenCode, every model of every provider it is set up for, as `provider/model`), then the reasoning effort. `/effort high` sets the effort directly.
- **Approvals** appear inline and in the inbox (`i`): `y` allow, `a` always, `n` deny, `e` edit.
- **Files.** Type `@` and part of a path anywhere in a message to pick a file from the thread's folder (git's file list, or the folder outside git); it goes in as `@path`.
- **Images.** `ctrl+v` pastes one from the clipboard; pasting or dropping an image path attaches it.
- **Queue and steer.** Messages sent while an agent works wait above the composer; click one to steer the agent with it now.
- **Edit.** `↑` on an empty composer, or a click on your message, edits it and resends it marked as edited.
- **Context.** Each agent's context window use shows in the chip line.
- **Changes.** `d` (from the transcript), `/changes` or a click on the header's `3 files +40 −12` opens every file that differs from the moment you sent a message, whoever changed it, with its diff and a ◆ for each agent that touched it. `[` `]` pick which of your messages to measure from. `u` puts the selected file back as it was then, `U` puts back all of them (each asks for a second press), `z` undoes that. The agents are told at their next turn.

| mode | Claude Code | Codex | OpenCode |
| --- | --- | --- | --- |
| `◇ plan` | `--permission-mode plan` | read-only sandbox | its plan agent; edits and commands declined |
| `● ask` | prompts come to Deck's inbox | `on-request`, `workspace-write` | edits, commands and fetches ask Deck first |
| `▲ yolo` | `--permission-mode bypassPermissions` | `never`, `danger-full-access` | Deck approves everything |

- **Threads.** `ctrl+k` jumps to any thread by name. `/rename`, `/pin` (pinned threads stay on top), `/done` (resolves a thread; any new message wakes it), `/archive` (off the sidebar, `/resume` brings it back) and `/delete`. `f` on one of your messages forks the thread there into a new one, with that message in the composer to change; `/fork` copies the whole thread. A fork's agents start fresh sessions, briefed on the history. `/move <path>` moves a thread to another folder; its agents restart there the same way. Each thread keeps its own unsent draft, and a dot marks threads with news you have not read.
- **Isolation (opt-in).** Threads share one working tree by default. `/isolate` gives a thread its own git worktree on a `deck/<id>` branch, made from your last commit; the chip line shows `⑂ deck/<id>`. `/apply` brings its changes into your tree as uncommitted edits (your branch and index are left alone), and `/unisolate` moves it back and removes the worktree, asking twice if there is work you never applied. Worktrees live in `~/.deck/worktrees`.
- **Small things.** A faint line ends every turn (`42s · 1.2k tokens · 3 files · $0.04`). Scrolled up, a `↓ 3 new` marker takes you back. The terminal title says what is working and what needs you, and when the terminal is in the background a finished turn or an approval raises a desktop notification where your terminal supports it. `?` lists every key.

Commands: `/new`, `/resume`, `/model`, `/effort <level>`, `/cd <path>`, `/name <name>`, `/changes`, `/rename`, `/pin`, `/done`, `/archive`, `/delete`, `/fork`, `/move <path>`, `/isolate`, `/apply`, `/unisolate`. `ctrl+c` interrupts; twice quits.

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

Agents are drawn with their own CLI's mark: Claude Code's `✻`, Codex CLI's `>_` and OpenCode's `▯`. Terminals that can show images get the real logos: inside Deck in Kitty and Ghostty, and in `deck doctor` there as well as in Windows Terminal (Sixel), iTerm2 and WezTerm. `DECK_IMAGES=off` turns them off; `DECK_IMAGES=kitty|sixel|iterm` picks a protocol by hand.

## How it works

Deck never calls a model API and never touches your credentials. It starts your CLIs as child processes and reads their structured output: `claude -p` with stream-json, `codex app-server` over JSON-RPC, and `opencode acp` over the Agent Client Protocol. For OpenCode, Deck sets one thing, in the child's environment only (`OPENCODE_CONFIG_CONTENT`, laid over any you already set): edits, commands and fetches ask first, so Deck's approvals and modes decide them. Your OpenCode config files are never written. Approvals and agent messages travel through a small MCP helper (`deck mcp-approve`) to Deck over loopback, authenticated with a per-run token. Usage counts against your own Claude and Codex plans and whatever providers OpenCode uses.

In a git work tree, each message you send records the tree as a git snapshot (tracked edits and untracked files that are not ignored). The snapshot is written through a throwaway index, so your branch, index, stash and refs are never touched. Its objects sit unreferenced in `.git` until `git gc` prunes them. Undo writes files the way `git checkout` would. Set `track_changes` to `false` to turn this off. Outside git the changes panel shows what the agents reported, without undo.

Deck is not affiliated with Anthropic or OpenAI.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports: [SECURITY.md](SECURITY.md).

## License

MIT
