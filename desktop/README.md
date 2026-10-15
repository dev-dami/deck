# Deck desktop

The native Deck app, built with [Gio](https://gioui.org) (pure Go, no webview). It is a window over the `deck` CLI: for each open project it runs `deck serve --rpc` and talks to it over gRPC on a local socket (`proto/deck/v1`).

```
go run . [project dir...]
```

- `backend/` starts and stops the engines. It reads each engine's handshake, dials it, and keeps an event stream that resumes after a drop. It uses the `deck` next to the app (or in its `bin/` folder) first, then the one on PATH.
- `state/` holds what is open: sessions, messages, approvals and queues, kept current by events.
- `ui/` is the window: projects and threads on the left, then the chat, approval cards and the composer.

It is its own Go module, so Gio stays out of the CLI binary. On Windows, build the window app with `go build -ldflags -H=windowsgui`.
