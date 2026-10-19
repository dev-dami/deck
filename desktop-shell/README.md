# Deck desktop shell

A desktop window onto a running Deck. It shows the sessions (threads) of a
project, the agents in each thread and their work, and it lets you run many
agents at once. The layout is a sidebar of sessions, a chat timeline, a
composer with an agents strip, and a side panel for changes, approvals and
the queue.

The app is a client of the `deck remote` server. It speaks protocol v2 of
[docs/remote-contract.md](../docs/remote-contract.md), the same protocol as
the phone client in `pkg/remote/web`. It adds nothing to the protocol.

![The main window](docs/screenshot.png)

## Run it

You need Node 22 and a `deck` binary.

```sh
cd desktop-shell
npm ci
npm run build
npm start
```

The app opens on the "Connect to Deck" screen. Connect in one of two ways:

- **Pair with a running Deck.** In your project, run `deck remote`. In
  another terminal, run `deck remote pair`. Paste the link it prints
  (`http://127.0.0.1:7878/#pair=…`) and click Pair. For another computer,
  run `deck remote --lan` there and paste the invite line
  (`deck://pair?…`). The invite carries the certificate fingerprint.
- **Start Deck here.** When `deck` is on your PATH, click "Start Deck in a
  folder…" and pick the project. The app runs `deck remote` in that folder,
  gets a code from `deck remote pair --json` and pairs. The servers that the
  app starts stop when the app quits.

The app remembers each pairing. On the next start it opens the newest one.

To work on the renderer with hot reload, run `npm run dev` and start
Electron with `DECK_DESKTOP_DEV_URL=http://localhost:5173 npm start`.

## Use it

- **Sessions.** Click a session to open it. A pulsing dot means that agents
  work in it. A still accent dot means new messages. A number means approvals wait.
  Hover a session to rename or delete it.
- **Many agents.** Type `$` in the composer to pick an agent. `$codex`
  reaches a free codex or starts one. `$codex+` always starts another one.
  `$codex*` reaches every codex. `$all` reaches every agent. "+ Add agent"
  above the composer writes `$kind+ ` for you.
- **Agents strip.** Each agent shows its kind, its model and its state:
  busy, waiting (for an approval) or idle. Click the model to change it.
  Click Stop to interrupt one agent.
- **Side panel.** Changes shows the diff of each changed file. Approvals
  lists the approvals of every session: Allow, Allow for session or Deny.
  Queue shows the messages that wait for a busy agent: send now or cancel.
- **Mode.** Plan, Ask or Yolo for each session. Yolo shows only when
  `deck remote devices --allow-yolo <id>` gave this app that right.
- **Keys.** Ctrl+K (Cmd+K) opens the command palette. Ctrl+N (Cmd+N) makes a
  new session. Enter sends. Shift+Enter adds a line.
- **Images.** Paste an image into the composer to send it.

## Build, test and package

| Script | What it does |
|---|---|
| `npm run dev` | Vite dev server for the renderer |
| `npm run build` | Renderer into `dist/renderer`, main and preload into `dist/app` |
| `npm start` | Electron on the build |
| `npm test` | Unit tests (Vitest) |
| `npm run e2e` | Build, then Playwright: the renderer in Chromium, and the whole app in Electron |
| `npm run smoke` | Build, then open the window, check that the app mounts, and quit |
| `npm run lint` | `tsc --noEmit` for the renderer and for main |

On a Linux machine without a display, run `xvfb-run -a npm run e2e` and
`xvfb-run -a npm run smoke`. As root in a container, Chromium needs
`--no-sandbox`; the Electron e2e test adds it for root only.

The tests use a fake Deck (`test/fake-server.ts`): the pair route and the
protocol v2 socket over an in-memory project. It decodes every request with
the shared guards, so a malformed call fails the test.

Packaging is not set up. electron-builder can package `dist/`,
`package.json` and the `ws` dependency; it needs no other file.

## Architecture

```
renderer (React, sandboxed)          main process (Node)                Deck
  shared Controller + RpcClient  --IPC-->  host.ts: pair, servers  --HTTP POST /api/v1/pair-->
  BridgeSocket (a SocketLike)    <-IPC---  link.ts: one WebSocket  <==WS /api/v1/ws (pinned)==>
```

- `src/main/` is the main process. `host.ts` is the one surface the window
  calls, and it checks every argument. `pin.ts` connects and pins the
  certificate. `pair.ts` posts the code. `link.ts` owns each WebSocket and
  relays frames. `servers.ts` keeps the pairings. `local.ts` starts
  `deck remote`. `main.ts` wires these into Electron.
- `src/preload/preload.ts` exposes `window.deck`: the host methods over IPC
  and a stream of socket events. Nothing else.
- `src/renderer/` is the window. It uses the phone client's controller,
  RPC client and reducer unchanged. `bridgeSocket.ts` gives the RPC client
  a socket that main owns. The desktop adds the layout, the `$` completion
  (`completion.ts`), the approvals inbox and agent states (`approvals.ts`),
  agent colours (`agents.ts`) and unread tracking (`ui.ts`).
- `src/shared/` holds the bridge types and the CSP.

### Shared code

The renderer imports the phone client's sources from `pkg/remote/web/src`
through the `@web` alias: `protocol.ts` (wire types and guards), `rpc.ts`,
`controller.ts`, `state.ts`, `markdown.ts`, `diff.ts`, `theme.ts` and a few
small helpers. They are not copied. One copy of the guards means one place
to change, and `bun test` in `pkg/remote/web` already checks that copy
against the fixtures in `pkg/remote/wire/testdata`. A protocol change that
breaks the desktop fails `npm run lint` here.

## Security

- **The token stays in main.** The window never sees it. The renderer's
  `hello` carries a placeholder, and `link.ts` writes the real token in
  before the frame leaves. After the hello, main sends only `req` frames
  from the window. When Deck closes with 4401 `token_invalid` or 4403
  (revoked), main forgets the token.
- **Token storage.** Main seals each token with Electron `safeStorage` and
  writes `servers.json` (mode 0600) in the app's data folder. When the
  system has no keychain (`basic_text` on Linux), main writes no token to
  disk. The pairing then lasts until the app quits, and the connect screen
  marks it "until the app quits".
- **Certificate pinning.** A `--lan` Deck uses a self-signed certificate.
  Main opens the TLS connection itself, compares the SHA-256 fingerprint of
  the certificate with the pin from the invite (or the one you typed), and
  only then sends a byte: the pair code or the WebSocket upgrade. A wrong
  certificate stops the connection. Plain HTTP is allowed to loopback only.
  `session.setCertificateVerifyProc` also accepts only a pinned certificate,
  for the case that Chromium ever connects.
- **No Node in the window.** `contextIsolation`, `sandbox` and no
  `nodeIntegration`. The preload exposes a fixed list of methods.
- **CSP.** The page loads from `app://deck` with `default-src 'none'`,
  `script-src 'self'`, `style-src 'self'` and `connect-src 'none'`: the
  window cannot open a network connection of its own. Main sends the policy
  as a header, and the built page carries it as a meta tag.
- **Safe rendering.** Agent text becomes a node tree (the shared
  `markdown.ts`), never HTML. A test bans `innerHTML` and its relatives in
  `src/`, as in the phone client. Links open in the system browser, and
  only http(s) links are links.
- **Locked window.** The window cannot navigate away or open a new window.
  Permission requests (camera, notifications and others) are denied.
