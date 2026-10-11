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
curl -fsSL https://raw.githubusercontent.com/dev-dami/deck-releases/main/install.sh | sh
```

Windows (PowerShell):

```powershell
powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/dev-dami/deck-releases/main/install.ps1 | iex"
```

Both install the latest release (checked against its checksum): to `~/.local/bin` on macOS and Linux, and to `%LOCALAPPDATA%\deck\bin` on Windows, which is added to your PATH. `DECK_VERSION=v0.8.0-dev` picks a release (pre-releases are only installed by name) and `DECK_INSTALL_DIR` another folder. Releases are published to the public [deck-releases](https://github.com/dev-dami/deck-releases/releases) repository.

## Releasing

Actions → **Release** → Run workflow, version `v0.8.0` (or `gh workflow run release.yml -f version=v0.8.0`), or push a `v*` tag. The workflow tags, builds macOS/Linux/Windows for amd64 and arm64, publishes the archives with `checksums.txt`, downloads them on each system and runs `deck --version`, then copies the release to the public `dev-dami/deck-releases` repository (needs the `RELEASES_TOKEN` secret). A suffix (`-dev`, `-rc.1`) makes it a pre-release.

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

| mode | Claude Code | Codex | OpenCode | Antigravity |
| --- | --- | --- | --- | --- |
| plan | `--permission-mode plan` | read-only sandbox | its plan agent | `--mode plan` |
| ask | asks you through Deck | `on-request`, `workspace-write` | edits, commands and fetches ask you | default mode; tools needing approval are denied with a visible notice (no Deck prompts) |
| yolo | `bypassPermissions` | `never`, `danger-full-access` | everything approved | `--mode accept-edits` + `--dangerously-skip-permissions` |

### Sidebar

The sidebar is a strip of panes in columns, like the [niri](https://github.com/YaLTeR/niri) window manager: each column stacks panes and gives each the height it needs, and the strip scrolls sideways to the column you are in. On terminals 160 columns wide or more it widens to show columns side by side. `ctrl+b` hides it; `l`/`→` from the chat (or a click) moves into it; `alt+h`/`alt+l` move between its columns from anywhere.

In the sidebar: `h` `l` (`←` `→`) move between columns, `h` from the first one goes back to the chat; `j` `k` (`↓` `↑`) scroll a pane, then move to the next one; `pgup` `pgdn` `g` `G` scroll; `x` (`ctrl+w`) closes a pane and `o` reopens one; `[` `]` move a pane into the column on that side, or out into a column of its own; `,` `.` (`H` `L`) move the whole column; `-` `=` make it narrower or wider; `esc` goes back to the editor. The mouse works too: click a pane, click `×` to close it, wheel to scroll it, shift+wheel to scroll the strip. The layout is saved in the global config (`options.tui.sidebar_layout`); `Sidebar: reset layout` in `ctrl+p` puts the default back.

### Git and GitHub

`ctrl+x` opens the git panel (also `Git` in `ctrl+p`). Its tabs (`tab` or `1` `2` `3`):

- **Branch**: the branch, `↑ahead ↓behind`, changed files and its pull request; commit all, push (sets the upstream on the first push), fetch, open or view the branch's pull request.
- **Pull requests**: the open ones, yours first, each with its checks (`✓` `✗` `●`). `enter` opens one: `l` shows the failed log, `a` puts it in the composer for `$claude` (or your default agent; change the name before you send), `r` re-runs the failed jobs, `m` merges after asking squash, merge or rebase, `c` checks it out, `o` opens it in the browser. Pending checks refresh every 20 seconds.
- **Recent**: branches by last commit, pushed or not, with their pull request. `enter` checks one out (asking first when you have uncommitted changes); `p` opens a pull request for it.

`p` opens the pull request form: title (the last commit), base (the default branch), a body listing the commits, draft. `ctrl+s` sends it; Deck pushes first when the branch needs it.

The header and sidebar show the branch's pull request and checks (`#12 ✓`, `#12 ● 3/5`). When checks finish, the status line says so and the bell rings (`bell` in the config). A branch pushed in the last hour without a pull request gets a one-line hint above the composer; `esc` in the panel hides it.

GitHub goes through your own [`gh`](https://cli.github.com) CLI; Deck never reads your token. Without gh, install it and run `gh auth login`; the git half works either way.

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
  "antigravity_binary": "agy",
  "models": { "claude": "", "codex": "", "opencode": "", "antigravity": "" },
  "efforts": { "claude": "", "codex": "", "opencode": "", "antigravity": "" }
}
```

`DECK_REDUCED_MOTION=1` turns animations off.

Agents are drawn with their own CLI's mark: Claude Code's `✻`, Codex CLI's `>_` and OpenCode's `▯`. `deck doctor` shows the real logos in terminals that can draw images (Kitty, Ghostty, Windows Terminal, iTerm2, WezTerm). `DECK_IMAGES=off` turns them off.

The interface's own settings (theme, notifications, mouse) live in `~/.config/deck/deck.json` and the project's `.deck/deck.json`; the `Themes` command switches between Deck's dark and light themes.

### Settings and usage

The `Settings` command (`ctrl+,` in terminals that report modified keys) changes both files from inside Deck: theme, motion, bell, default mode, compact mode, background, mouse and exit banner on **General**; the default agent, change tracking, telling agents your name, and where each agent CLI was found on **Agents**. `tab` or `1`–`3` switch pages.

The **Usage** page is a short token summary — totals, a bar per agent and a daily sparkline, over today, 7 days, 30 days or all time (`←`/`→`). `alt+u` opens the full view from there.

### Usage

`alt+u` (or the `Usage` command in `ctrl+p`) opens the full Usage view. Its tabs (`tab`/`shift+tab` or `1`–`6`), over today by hour, or 7 days, 30 days or all time by day (`t` `w` `m` `a`):

- **Overview**: tokens, turns, agent time, cache hit rate, and the average and 95th-percentile time to first token; tokens per agent over the period (`←`/`→` or the mouse move a crosshair that reads every agent at once); the share of input, cached, cache-write, output and reasoning tokens; and a 26-week heatmap.
- **Limits**: each agent's plan and its windows (5-hour, weekly, …) as meters of what is left, with when each resets and whether you are using it faster than time passes (`▲` `●` `▼`). Claude Code and Codex report them; OpenCode and Antigravity do not, and say so. `j`/`k` pick a window to chart its history.
- **Behaviour**: how often you cancelled, steered, allowed or denied approvals (by hand or automatically), relayed, paused hops, hit errors, and changed modes, models or effort, each with a daily sparkline; and the cancel, deny and relay rates.
- **Traces**: the recent turns with their agent, model, tokens, time to first token, length and how they ended. `enter` shows where a turn's time went as a waterfall of waiting, thinking, writing and tool calls; the tokens per span are estimates.
- **Models**: tokens, cache hit rate, time to first token and turns per model; the selected one shows its tokens per turn.
- **Cost**: the only place with dollars, as each CLI reported them. Deck has no pricing of its own: a CLI that reports no cost (Codex, Antigravity) shows `—`, not $0.

The **Usage** pane in the sidebar shows the session's agents' plan meters, today's tokens per agent, what each is doing now and its last turn; `enter` opens the full view. Every finished turn, the plan windows as they move, and what you did to steer the agents are recorded in Deck's local database; nothing is sent anywhere, no prompt text is kept, and turns from before this version are not counted.

## Agents and Deck

Every agent Deck runs (Claude, Codex, OpenCode, Antigravity) is connected to Deck through one MCP server, `deck`, that offers just two tools:

- **`search {query?, limit?}`** finds things. It returns the Deck ops that match, the best matching skills (with the agent they come from) and hits from this project's past threads. An empty query lists every op with its arguments.
- **`execute {op, args?}`** runs one op. An unknown op or bad arguments come back as an error that points at `search`.

Capabilities are ops, not tools, so adding one never adds a tool:

| Op | What it does |
| --- | --- |
| `thread.send {to, message}` | Message another agent in the thread, or `all`. It shows in the thread like a `$name` mention. |
| `thread.read` | The current shared thread. |
| `history.threads {limit?}` | This project's Deck threads: id, title, updated time, agents. |
| `history.search {query, limit?}` | Search what was said in this project's threads. |
| `history.read {thread, last?}` | The last entries of one of them. |
| `skills.list` | Every skill any agent can see: Deck's builtins, and the project's and your `.claude`, `.agents`, `.codex`, `.opencode`, `.agent`/`.gemini` skill folders. |
| `skills.read {name, source?, file?}` | A skill's `SKILL.md`, or one file inside its folder, so one agent can follow another's skill. |
| `skills.used` | Skills the agents in this thread have used (Claude's Skill tool, `SKILL.md` reads). |
| `git.status`, `git.log {n?}`, `git.diff {path?, staged?}`, `git.show {ref}`, `git.branches` | Read-only git, in the thread's folder. |
| `gh.prs`, `gh.pr {number}`, `gh.runs {limit?}` | Pull requests, their checks and workflow runs, through your own `gh`. |
| `deck.jobs` | Commands the agents are running and shells left in the background. |

Everything is read-only except `thread.send`; agents use their own shells for changes. Results are capped at 20 KB. History is shown as text only, without tool output. Anything shaped like a token is redacted. Git and gh run with Deck's clean environment, and Deck never reads their credentials. Claude also gets `approve`, the permission-prompt tool.

## How it works

Deck never calls a model API and never touches your credentials. It starts your CLIs as child processes and reads their structured output: `claude -p` with stream-json, `codex app-server` over JSON-RPC, and `opencode acp` over the Agent Client Protocol. For OpenCode, Deck sets one thing, in the child's environment only (`OPENCODE_CONFIG_CONTENT`, laid over any you already set): edits, commands and fetches ask first, so Deck's approvals and modes decide them. Your OpenCode config files are never written. Approvals and the agents' `search` and `execute` calls travel through a small MCP helper (`deck mcp-approve`) to Deck over loopback, authenticated with a per-run token. Usage counts against your own Claude and Codex plans and whatever providers OpenCode uses.

In a git work tree, each message you send records the tree as a git snapshot (tracked edits and untracked files that are not ignored). The snapshot is written through a throwaway index, so your branch, index, stash and refs are never touched. Its objects sit unreferenced in `.git` until `git gc` prunes them. Undo writes files the way `git checkout` would. Set `track_changes` to `false` to turn this off. Outside git the changes panel shows what the agents reported, without undo.

Deck is not affiliated with Anthropic or OpenAI.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports: [SECURITY.md](SECURITY.md).

Commits that agents make in a Deck thread credit [Deck](https://github.com/deck-cli) (`Co-Authored-By: Deck <311780591+deck-cli@users.noreply.github.com>`); see Settings → Agents → Deck attribution.

## License

Copyright © 2026 dev-dami. All rights reserved. Third-party license terms are in [LICENSE.crush.md](LICENSE.crush.md).
