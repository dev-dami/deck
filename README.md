# ◆ deck

one deck for all your agents

<!-- demo gif: docs/demo.gif -->

## What it is

Deck is one terminal app for running Claude Code and Codex side by side. It is a visual and communication layer over the CLIs you already have installed. A thread holds you and as many agents as you like, so Claude and Codex can work on the same problem and talk to each other where you can see it. Every agent, its tool calls, file diffs and approval requests show up in one place.

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

This runs the full TUI against scripted fake agents. It needs no CLI and no login, and it never touches `~/.deck/config.json`. It opens on the logo with an empty thread. Type anything to start the duo: a scripted Claude plans a change and asks a scripted Codex to review it, Codex answers, and Claude acts on it. Or type `@claude plan the store refactor, @codex review it` to start both at once.

To look at the animated logo on its own:

```sh
go run ./cmd/decklogo
```

Move the mouse over it for ripples, click for a splash, `r` replays the intro, `q` quits. With `DECK_REDUCED_MOTION=1` it is static. The same logo is the empty state of Deck itself: it ripples under the pointer, any keypress calms it, and it stops drawing frames once it is at rest.

## Using it

Type in the composer at the bottom and press enter. The sidebar lists your threads, not your agents. A thread is a conversation you share with one or more agents; with a single agent it behaves like a plain agent session.

### Addressing agents

Every message goes through the thread you are in. While you type, a faint hint at the right of the line above the composer says where the text will go.

- `fix the build` with no mention goes to whoever spoke last. A thread with one agent sends to that agent. In an empty thread it starts a new agent of the default kind.
- `@codex review this` goes to that member. The leading mention is not sent; the agent gets `review this`.
- `@claude do the plan, @codex review it` addresses both. Both receive the text after the leading mention (mentions further in stay in it), and they work at the same time.
- `@all stop for now` goes to everyone in the thread.
- `@codex` where the thread has no Codex adds one (hint `+ adds codex`). `@codex` on its own just adds it without sending anything.
- A mention only counts when it starts a clause: at the start of the text or of a line, after `.` `!` `?` `;` `:` or a comma, after `then`, `and`, `also` or `plus`, or right after another mention. `ask @codex about it`, `me@example.com` and `@codex.go` are plain text.
- `@name` matches an agent's id or name, ignoring case. Failing that, a kind (`claude`, `codex`) addresses the first agent of that kind, or adds one if the thread has none. A name that matches nothing is refused and your text stays in the composer.
- A message to an agent that is mid-turn is queued and delivered when its turn ends.

Typing `@` or `/` opens a menu above the composer. `@` lists the members of this thread first, then `@all` (with two or more members), then the kinds the thread does not have yet, labelled `add codex`. Tab completes, enter picks, esc dismisses.

### Talking to each other

Every agent in a thread sees everything: your messages, the other agents' replies, and one-line summaries of what they did with tools. Agents reach each other in two ways: with Deck's `send_message` tool, or by starting a line of their reply with `@name`. Both show up in the transcript as a quiet line between the two blocks:

```
↪ claude → codex  "can you review this plan?"
```

Nothing is relayed out of your sight. Long messages fold to a few lines; enter on the line (transcript focus) unfolds it. The messaging tool calls themselves stay out of the transcript, and an agent reading the thread shows as `∴ read the thread`.

After each message of yours, agents may pass up to six messages between themselves. Then the thread pauses:

```
‖ paused after 6 agent messages · enter continue · or type to steer
```

The message that hit the limit is shown dimmed with a `held` tag and is not delivered. Press enter on an empty composer to continue: the held messages are delivered and the marker becomes `continued`. Or type a message instead: it steers the thread, and the held messages are dropped from delivery (they stay in the transcript).

Agents learn your name only if `tell_agents_name` is `true` in the config.

### Slash commands

| command | what it does |
| --- | --- |
| `/new` | a fresh, empty thread |
| `/resume` | open saved threads and continue one |
| `/model <name>` | model for the next agent of the kind you are talking to; `/model codex <name>` names the kind; `default` clears it |
| `/cd <path>` | directory for new threads; `~` works |
| `/name <name>` | your display name; saved to `config.json` |

### Keys

Composer, the default focus:

| key | action |
| --- | --- |
| enter | send; on an empty composer, continue a paused thread (or start the default agent in an empty one) |
| alt+enter, ctrl+j | new line; a trailing `\` before enter also adds one |
| up / down | move the thread selection when the composer is empty, otherwise move between lines |
| tab, esc | move focus to the transcript |
| ctrl+c | interrupt every agent that is working in the thread; otherwise clear the composer; press twice within a second to quit |
| i | open the approval inbox, when approvals are pending and the composer is empty |
| ctrl+w, ctrl+u, ctrl+k | delete word back, to start of line, to end of line |
| ctrl+a / ctrl+e | start / end of line |
| alt+left / alt+right | move by word |

Transcript, after tab or esc:

| key | action |
| --- | --- |
| j / k, up / down | select thread |
| pgup / pgdown, ctrl+u / ctrl+d | scroll half a page |
| g / G | top / bottom |
| [ / ] | move between foldable items |
| enter or space | fold or unfold the item under the cursor |
| n | new thread; denies instead when the thread has a pending approval |
| y / a / e | approve, always approve, or edit, for the thread's first pending request |
| i | approval inbox |
| q | quit, when the composer is empty |
| tab, esc | back to the composer |

Inbox and resume lists: up / down or j / k to move, enter to open, esc or q to close.

Mouse: the wheel scrolls the transcript, a click selects a thread in the sidebar, a click on a fold toggles it, and a click on the composer focuses it. On the empty screen the logo follows the pointer.

### Approvals

When an agent asks to run a command or change a file, the request waits in the inbox, and its card appears in that agent's block of the thread.

- `y` allows it once.
- `a` allows it and allows that tool for the rest of the session.
- `n` denies it. Add an optional reason, enter sends it, esc cancels.
- `e` edits it. For a command, change the text and press enter. The protocol cannot carry an edited command, so Deck denies the original and tells the agent to run your version instead. An unchanged command is allowed. For other requests, your text goes back as "do this instead".

Press `i` to see pending approvals across all threads.

### Threads

Threads are saved under `~/.deck/threads`, with their members and what each said. `deck ls` lists them with name, agents, title, cost and age. `/resume` picks one to continue in the TUI: every agent in it is started again and its history is shown.

### Reduced motion

Motion is on by default. Turn it off with `"motion": "off"` in the config, or set `DECK_REDUCED_MOTION=1`, which overrides the config. Reduced motion snaps animations to their end state, shows the logo static, and stops the clock. Deck also switches to reduced motion on its own when frames run consistently slow.

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
| `tell_agents_name` | `false` | tell agents your name; when false they only know you as "the user" |
| `claude_binary` | `"claude"` | path or command name for Claude Code |
| `codex_binary` | `"codex"` | path or command name for Codex |
| `models.claude`, `models.codex` | `""` | model to run; empty means the CLI's default |

`/name` writes `user_name` back to the file. `/model` is not saved.

## How it talks to the agents

Deck never calls a model API. It starts your CLI as a child process and reads its structured output.

- **Claude Code** runs as `claude -p` with stream-json input and output. Permission prompts are routed with `--permission-prompt-tool mcp__deck__approve` to a small stdio MCP helper, `deck mcp-approve`, which Deck launches from its own binary. The helper forwards each request over loopback TCP (`127.0.0.1`) to Deck's approval broker. It authenticates with a token that Deck generates at startup and passes through the helper's environment, never its command line.
- **Codex** runs as `codex app-server` and speaks JSON-RPC 2.0 over newline-delimited stdio. Its approval requests arrive in the same inbox. Deck starts it with the `on-request` approval policy and the `workspace-write` sandbox.

- **Between agents.** Deck keeps one log per thread. Before an agent's turn it is told what it missed: your messages, the other agents' replies and relayed messages, and one-line summaries of their tool use. The same helper that carries approvals also serves two messaging tools, `send_message` and `read_thread`, through the broker; a line of a reply that starts with `@name` is relayed too. Every relay is shown in the transcript.

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
