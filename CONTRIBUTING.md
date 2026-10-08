# Contributing to deck

Thanks for taking the time to help. Deck is small on purpose, and we want it to stay that way. Bug reports, fixes, docs and well-scoped features are all welcome.

## Before you start

- For anything larger than a small fix, open an issue first so we can agree on the shape of the change.
- Read the [design principles](#design-principles) and [Playing by the rules](#playing-by-the-rules) below. They matter more than any style guide.
- Be kind. See the [code of conduct](CODE_OF_CONDUCT.md).

## Prerequisites

- Go 1.25 or newer, the version in `go.mod`.
- Git.
- Optional: the `claude` and `codex` CLIs, if you want to run Deck for real or record live sessions. You do not need them for the normal test suite.

## Build, vet and test

```sh
go build ./...
go vet ./...
go test ./...
gofmt -l .        # should print nothing
```

Run `gofmt -w` on any file it lists. Please run all four before you open a pull request.

## Try the UI without tokens

You can run the full TUI against scripted fake agents. It needs no CLI, no login and no usage:

```sh
go run ./cmd/deckdemo
```

To look at the animated logo on its own:

```sh
go run ./cmd/decklogo
```

The demo never touches `~/.deck/config.json`, so it is safe to poke at.

## Live tests

Some tests drive the real `claude` and `codex` CLIs. They are skipped unless you opt in, because they spend your own usage:

| variable | what it runs |
| --- | --- |
| `DECK_LIVE=1` | record Claude and Codex sessions |
| `DECK_LIVE=approve` | the Claude approval path through the broker |
| `DECK_LIVE=message` | `send_message` against real Claude |
| `DECK_LIVE=2` | the Codex patch and approval session |

If you record a session, scrub it before you commit it. Recordings are raw CLI output and can include:

- file paths and your home directory,
- your username and hostname,
- account ids and organization ids,
- anything else the CLI echoed back.

Replace them with neutral placeholders such as `/home/user` and `example-host`. Check the diff by hand. Never commit a recording you have not read through.

## TUI golden files

TUI snapshots live in `internal/tui/testdata`, one directory per test, with one `.golden` file per terminal size. If your change alters the output on purpose, update them with the `-update` flag:

```sh
go test ./internal/tui -run TestGolden -update
```

Then read the diff in `git diff internal/tui/testdata` before you commit. A golden change should be explained in the pull request. If you did not expect a golden to change, that is a bug to fix, not a file to update.

## Design principles

Deck should feel calm and dense, like a good editor, not a dashboard.

- **Calm.** Little colour, little movement, no decoration for its own sake.
- **Dense.** Show the information people need in as few lines as possible.
- **No boxes.** Do not draw borders or cards around content. Use spacing, indentation and a single accent to group things.
- **One accent colour.** Everything else is neutral or a muted tint.
- **Motion is optional.** Every animation must respect `DECK_REDUCED_MOTION` and the `motion` setting. Animations must stop drawing frames once they reach rest.
- **Colours come from `internal/tui/theme`.** Do not write colour literals in other packages. Add a token to the theme if you need one, and make sure it works in both light and dark terminals.

## Playing by the rules

This is the part we will not compromise on.

- Deck launches only the `claude` and `codex` binaries the user has installed, under their own login.
- Deck never reads, copies or forwards credential files or OAuth tokens.
- Deck never calls the Anthropic or OpenAI APIs directly. It talks to the CLIs over their documented structured protocols.
- Agents do not approve their own actions. Every approval goes through the user, via the broker.

The guard test in `internal/guard` enforces the first three points. It fails `go test ./...` if a Go file in this module mentions a credential file, a model API host, or reads `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` from the environment. Pull requests that weaken, skip or work around that test will not be accepted. If you think the guard is wrong for a legitimate case, open an issue and talk to the maintainer before you write any code.

## Commit style

- Write the subject in the imperative mood, as if it completes the sentence "This commit will...": `Add thread export`, `Fix resume after pause`.
- Keep the subject short, around 72 characters or fewer. Use the body to say why, not what.
- One logical change per commit where you can.

## Pull request checklist

Use the checklist in the pull request template. In short:

- [ ] `go build ./...`, `go vet ./...` and `go test ./...` pass
- [ ] `gofmt -l .` prints nothing
- [ ] Golden files are updated on purpose, and the diff has been read
- [ ] No credentials, personal data or unscrubbed recordings in fixtures
- [ ] Docs (README and this file, where relevant) are updated
- [ ] The change keeps the rules in "Playing by the rules"

## License

By contributing you agree that your contribution is licensed under the MIT license in [LICENSE](LICENSE).
