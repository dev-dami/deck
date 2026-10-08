# ◆ deck

one deck for all your agents

<!-- demo gif: docs/demo.gif -->

## What it is

Deck is one terminal app for running Claude Code and Codex side by side. It is a visual and communication layer over the CLIs you already have installed. Every agent, its tool calls, file diffs and approval requests show up in one place, and you message any running agent with `@name`.

Inspired by T3 Code, built for the terminal.

## Install

```sh
go install github.com/dev-dami/deck/cmd/deck@latest
```

Needs Go 1.25 or newer.

Deck drives the `claude` and `codex` CLIs. Install them and sign in yourself. Deck never logs in for you:

```sh
claude auth login   # Claude Code
codex login         # Codex CLI
```

Then check what Deck can see:

```sh
deck doctor
```

Doctor reports whether each CLI is installed, its version, and whether it is signed in. It runs each CLI's own status command and prints only "signed in" or "not signed in".

## Try it without spending tokens

```sh
go run ./cmd/deckdemo
```

This runs the full TUI against scripted fake agents. It needs no CLI and no login, and it never touches `~/.deck/config.json`.

## Using it

Type in the composer at the bottom and press enter.

### Addressing agents

- `fix the build` goes to the selected agent. With no agents running, or in new-agent mode, it starts a new agent of the default kind.
- `@claude` or `@codex` on its own selects a running agent of that name. If none is running, the next message starts a new agent of that kind.
- `@claude fix the build` sends to a running agent named `claude`. If none is running, it starts a new Claude agent with that prompt.
- `@name message` sends to a running agent by name, from anywhere. `@name` alone selects it.

Typing `@` or `/` opens a menu above the composer. Tab completes, enter picks, esc dismisses.

### Slash commands

| command | what it does |
| --- | --- |
| `/new` | start a fresh agent |
| `/resume` | open saved threads and continue one |
| `/model <name>` | model for the next agent of the selected kind; `default` clears it |
| `/cd <path>` | directory for new agents; `~` works |
| `/name <name>` | your display name; saved to `config.json` |

### Keys

Composer, the default focus:

| key | action |
| --- | --- |
| enter | send |
| alt+enter, ctrl+j | new line; a trailing `\` before enter also adds one |
| up / down | move the agent selection when the composer is empty, otherwise move between lines |
| tab, esc | move focus to the transcript |
| ctrl+c | interrupt a working agent; otherwise clear the composer; press twice within a second to quit |
| i | open the approval inbox, when approvals are pending and the composer is empty |
| ctrl+w, ctrl+u, ctrl+k | delete word back, to start of line, to end of line |
| ctrl+a / ctrl+e | start / end of line |
| alt+left / alt+right | move by word |

Transcript, after tab or esc:

| key | action |
| --- | --- |
| j / k, up / down | select agent |
| pgup / pgdown, ctrl+u / ctrl+d | scroll half a page |
| g / G | top / bottom |
| [ / ] | move between foldable items |
| enter or space | fold or unfold the item under the cursor |
| n | new agent; denies instead when the selected agent has a pending approval |
| y / a / e | approve, always approve, or edit, for the selected agent |
| i | approval inbox |
| q | quit, when the composer is empty |
| tab, esc | back to the composer |

Inbox and resume lists: up / down or j / k to move, enter to open, esc or q to close.

Mouse: the wheel scrolls the transcript, a click selects an agent in the sidebar, a click on a fold toggles it, and a click on the composer focuses it.

### Approvals

When an agent asks to run a command or change a file, the request waits in the inbox.

- `y` allows it once.
- `a` allows it and allows that tool for the rest of the session.
- `n` denies it. Add an optional reason, enter sends it, esc cancels.
- `e` edits it. For a command, change the text and press enter. The protocol cannot carry an edited command, so Deck denies the original and tells the agent to run your version instead. An unchanged command is allowed. For other requests, your text goes back as "do this instead".

Press `i` to see pending approvals across all agents.

### Threads

Threads are saved under `~/.deck/threads`. `deck ls` lists them with name, agent, title, cost and age. `/resume` picks one to continue in the TUI.

### Reduced motion

Motion is on by default. Turn it off with `"motion": "off"` in the config, or set `DECK_REDUCED_MOTION=1`, which overrides the config. Reduced motion snaps animations to their end state and stops the clock. Deck also switches to reduced motion on its own when frames run consistently slow.

## Config

Settings live in `~/.deck/config.json`. A missing file means defaults, and any key you leave out keeps its default. Invalid values make Deck exit with an error.

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

| key | default | meaning |
| --- | --- | --- |
| `user_name` | `git config --global user.name`, else your OS user name | name shown in the UI |
| `default_agent` | `"claude"` | kind started for a message with no agent; `"claude"` or `"codex"` |
| `motion` | `"on"` | `"on"` or `"off"` |
| `bell` | `true` | ring the terminal bell when a turn needs attention |
| `tell_agents_name` | `false` | tell agents your name |
| `claude_binary` | `"claude"` | path or command name for Claude Code |
| `codex_binary` | `"codex"` | path or command name for Codex |
| `models.claude`, `models.codex` | `""` | model to run; empty means the CLI's default |

`/name` writes `user_name` back to the file. `/model` is not saved.

## How it talks to the agents

Deck never calls a model API. It starts your CLI as a child process and reads its structured output.

- **Claude Code** runs as `claude -p` with stream-json input and output. Permission prompts are routed with `--permission-prompt-tool mcp__deck__approve` to a small stdio MCP helper, `deck mcp-approve`, which Deck launches from its own binary. The helper forwards each request over loopback TCP (`127.0.0.1`) to Deck's approval broker. It authenticates with a token that Deck generates at startup and passes through the helper's environment, never its command line.
- **Codex** runs as `codex app-server` and speaks JSON-RPC 2.0 over newline-delimited stdio. Its approval requests arrive in the same inbox. Deck starts it with the `on-request` approval policy and the `workspace-write` sandbox.

```
deck (TUI, session manager, approval inbox)
 |
 +-- claude adapter --> claude -p (stream-json, stdio)
 |                        | permission prompt
 |                        v
 |                      deck mcp-approve (stdio MCP helper)
 |                        | loopback TCP 127.0.0.1, token
 |                        v
 |                      approval broker --> inbox
 |
 +-- codex adapter ---> codex app-server (JSON-RPC 2.0, stdio)
                          approval requests --> inbox
```

## Playing by the rules

- Deck launches only the `claude` and `codex` binaries you installed, under your own login.
- It never reads, copies or forwards credential files or OAuth tokens.
- It never calls the Anthropic or OpenAI APIs directly.
- A guard test in `internal/guard` fails `go test ./...` if a Go file in this module mentions a credential file, a model API host, or reads `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` from the environment.
- Headless Claude Code use counts against your plan's usage limits, and Codex use counts against your Codex usage.
- Policies change. Read the current terms yourself: the [Anthropic help center](https://support.claude.com) and the [Codex docs](https://developers.openai.com/codex).

Deck is not affiliated with Anthropic or OpenAI.

## Development

```sh
go test ./...
```

Live tests record real sessions and spend a little of your usage, so they are skipped unless gated. `DECK_LIVE=1` records Claude and Codex sessions. `DECK_LIVE=approve` runs the Claude approval path. Codex's patch and approval session uses `DECK_LIVE=2`. TUI golden snapshots live in `internal/tui/testdata`.

## License

TBD
