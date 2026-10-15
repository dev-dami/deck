# Deck desktop

The native Deck app, built with [Gio](https://gioui.org) (pure Go, no webview). It is a window over the `deck` CLI: for each open project it runs `deck serve --rpc` and talks to it over gRPC on a local socket (`proto/deck/v1`).

```
go run . [project dir...]
```

- `backend/` starts and stops the engines. It reads each engine's handshake, dials it, and keeps an event stream that resumes after a drop. It uses the `deck` set in Settings, else the one next to the app (or in its `bin/` folder), then the one on PATH.
- `state/` holds what is open: sessions, messages, approvals, queues, changes and models, kept current by events.
- `ui/` is the window:
  - the rail: projects, threads, search and recents;
  - the chat: markdown, approvals and the composer with images;
  - the side panel: changes and diffs, agents with model and effort, and the queue;
  - Settings.
- `platform/` is the OS glue:
  - the system folder and file pickers (Windows: IFileOpenDialog through COM, no cgo);
  - Show in folder, VS Code and terminal;
  - launch at sign-in.
- `settings/` is the app's preferences, in `<user config>/Deck/settings.json`.

Shortcuts:

| Keys | Action |
|---|---|
| Ctrl+O | Open a folder |
| Ctrl+N | New thread |
| Ctrl+B | Show or hide the side panel |
| Ctrl+, | Settings |

It is its own Go module, so Gio stays out of the CLI binary. On Windows, build the window app with `go build -ldflags "-H=windowsgui -X main.version=…"`.
