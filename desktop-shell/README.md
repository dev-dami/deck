# Deck desktop shell

A desktop window onto Deck. It shows the sessions (threads) of a project,
the agents in each thread and their work, and it lets you run many agents
at once. The layout is a sidebar of sessions, a chat timeline, a composer
with its agents, and a side panel for the diff, approvals and the queue.

![The main window](docs/screenshot.png)

## Two ways to reach a Deck

| | Open a folder | Pair with a Deck |
|---|---|---|
| For | A project on this computer | A Deck on another computer, or one you started yourself |
| Deck command | The app runs `deck serve --rpc` | You run `deck remote` (`--lan` for another computer) |
| Transport | gRPC (`proto/deck/v1/deck.proto`) on a Unix socket | WebSocket, protocol v2 (`docs/remote-contract.md`) |
| Trust | A token made for this launch, printed on Deck's stdout | A one-time pairing code, then a device token |
| Lifetime | Deck stops when you close the folder or quit the app | Deck runs until you stop it |
| Events | Every session's events, live | The open session's messages only |
| Yolo mode | Allowed | Only with `deck remote devices --allow-yolo <id>` |

Use **Open a folder** for your own projects on this computer. It is the
default when `deck` is on your PATH. Use **Pair with a Deck** for a Deck on
another machine, or when Deck already runs in a terminal and you want to
watch it from the window too.

## Run it

You need Node 22 and a `deck` binary.

```sh
cd desktop-shell
npm ci
npm run build
npm start
```

The app opens the last folder when `deck` is on your PATH, else the newest
pairing, else the "Connect to Deck" screen:

- **Open a folder.** Click "Open a folder…" and pick the project, or click
  a recent folder. The app runs `deck serve --rpc` in that folder. Close the
  folder with "Switch Deck"; that stops Deck. The app remembers the last
  eight folders.
- **Pair with a Deck.** In the project, run `deck remote`. In another
  terminal, run `deck remote pair`. Paste the link it prints
  (`http://127.0.0.1:7878/#pair=…`) and click Pair. For another computer,
  run `deck remote --lan` there and paste the invite line (`deck://pair?…`).
  The invite carries the certificate fingerprint. The app remembers each
  pairing.

To work on the renderer with hot reload, run `npm run dev` and start
Electron with `DECK_DESKTOP_DEV_URL=http://localhost:5173 npm start`.

## Use it

The window is a remake of [T3 Code](https://github.com/pingdotgg/t3code)'s
(MIT) pages on Deck: the same sidebar, chat, composer, diff panel and
settings, with Deck's sessions as threads and Deck's agents in place of
T3's providers.

- **Projects.** The folder button in the sidebar switches between recent
  folders and paired Decks, or adds a project (Deck starts
  `deck serve --rpc` in it). One Deck is open at a time.
- **Threads.** Each card shows the project, the title, the last message and
  the agents that wrote. A thread that works says Working, one that waits
  for you says Approval, one that finished while you looked elsewhere says
  Done. Quiet threads older than three days fold into Settled. Double-click
  a title to rename it; hover a card to delete it.
- **New thread** (Ctrl+N, Cmd+N) opens the empty screen. The first message
  makes the thread, named after its first line.
- **Chat.** A finished reply folds its tool calls and thinking behind
  "Worked for …"; the answer stays in view. The thread's changed files show
  under the last reply; "Open diff" opens them in the diff panel.
- **Composer.** The agent picker (where T3 picks a model) sends the next
  message to any agent or to one of the thread's agents, starts another
  agent of a kind, and changes the model and effort of the agent it sends
  to. Typing `$` does the same: `$codex` reaches a free codex or starts one,
  `$codex+` starts another, `$codex*` reaches every codex, `$all` every
  agent. The strip under the composer holds the mode (Plan, Ask or Yolo)
  and each agent's state: busy, waiting (for an approval) or idle. While
  agents work and the prompt is empty, send turns into stop.
- **Approvals.** The open thread's approval sits on top of the composer:
  Decline, Always allow this session, or Approve. The Approvals panel lists
  those of every thread.
- **Side panel.** Diff shows every changed file with line numbers; Queue
  shows the messages that wait for a busy agent: send now or cancel.
- **Settings** (the gear, or Ctrl+,): send key, Yolo confirmation, folding,
  time format, light or dark (or Deck's theme, or the system's), card or
  compact thread rows, the agent kinds, the connection and the shortcuts.
- **Keys.** Ctrl+K (Cmd+K) searches threads and commands. Ctrl+B hides the
  sidebar. Enter sends. Shift+Enter adds a line.
- **Images.** Paste, drop or attach an image to send it.

## Build, test and package

| Script | What it does |
|---|---|
| `npm run dev` | Vite dev server for the renderer |
| `npm run build` | Renderer into `dist/renderer`; main, preload and `deck.proto` into `dist/app` |
| `npm start` | Electron on the build |
| `npm test` | Unit tests (Vitest) |
| `npm run e2e` | Build, then Playwright: the renderer in Chromium over both transports, and the whole app in Electron |
| `npm run smoke` | Build, then open the window, check that the app mounts, and quit |
| `npm run lint` | `tsc --noEmit` for the renderer and for main |

On a Linux machine without a display, run `xvfb-run -a npm run e2e` and
`xvfb-run -a npm run smoke`. As root in a container, Chromium needs
`--no-sandbox`; the Electron e2e tests add it for root only.

The tests use fakes in `test/`:

- `fake-server.ts` is a protocol v2 Deck: the pair route and the WebSocket
  over an in-memory project. It decodes every request with the shared
  guards, so a malformed call fails the test.
- `fake-grpc.ts` is a `deck serve --rpc`: the `deck.v1.Deck` service from
  the same `.proto`, on a Unix socket behind the token, over the same
  in-memory project.
- `fake-deck.cjs` stands in for the `deck` binary. It prints the fake gRPC
  server's handshake and lives until its stdin closes, as Deck does.

Packaging is not set up. electron-builder can package `dist/`,
`package.json` and the dependencies (`ws`, `@grpc/grpc-js`,
`@grpc/proto-loader`); it needs no other file.

## Architecture

```
renderer (React, sandboxed)                  main process (Node)                         Deck
  DeckClient ─┬─ Controller + RpcClient  ─IPC─  link.ts: one WebSocket, pinned  ══WS══  deck remote
              └─ RpcDeckClient           ─IPC─  decks.ts: gRPC client            ══gRPC══ deck serve --rpc
  shared reducer (AppState) → the UI             host.ts: the only surface, checks every argument
```

- **One state, two transports.** The UI renders the phone client's
  `AppState` and calls a small `DeckClient` interface
  (`src/renderer/client.ts`). Two classes implement it:
  - the phone client's `Controller`, unchanged, for protocol v2. Its socket
    is `BridgeSocket`, which main owns;
  - `RpcDeckClient` (`rpcClient.ts`) for a local Deck. It calls the
    `deck.v1` methods through main and maps each answer and event to the
    same entities (`rpcMap.ts`). Each mapped entity goes through the shared
    guard, so the state holds only what a v2 server could also send.
- **Main process** (`src/main/`): `host.ts` is the one surface the window
  calls. `decks.ts` starts `deck serve --rpc`, reads the handshake, dials
  the socket and keeps the process. `pin.ts`, `pair.ts` and `link.ts` are
  the v2 side. `servers.ts` keeps pairings, `recent.ts` the recent folders.
- `src/preload/preload.ts` exposes `window.deck`: the host methods over IPC
  and the socket and rpc event streams. Nothing else.
- `src/shared/` holds the bridge types and the CSP.
- **The UI** is React with Tailwind CSS v4, rebuilt after T3 Code's
  components (`Sidebar`, `ChatHeader`, `MessagesTimeline`, `ChatComposer`,
  `DiffPanel`, the settings layout). `src/renderer/styles.css` holds the
  theme tokens, `src/renderer/ui/kit/` the primitives (button, badge, menu)
  after T3's `components/ui`, and lucide-react draws the icons. Tailwind
  builds one stylesheet, so the CSP's `style-src 'self'` holds. The
  window's own settings (`prefs.ts`) live in its localStorage.

### What the rpc transport adds, and what the app uses

- **Every session's events.** The v2 hub sends message events only for
  the focused session. The rpc stream sends all of them, so a thread in the
  background fills in live and is marked unread as soon as an agent writes
  in it. Opening it needs no refetch.
- **Resume by (epoch, seq).** When the stream ends, the client subscribes
  again from its last event. A `Ready` with `resync` makes it refetch.
- **Yolo** is allowed: the caller is the person at this machine.
- **No caps.** Text and diffs are not cut; images travel as bytes.
- **Error messages.** A refused call carries Deck's own message; the app
  shows it in a toast.

`AgentLine` is the same in both: `busy` only, no richer status.

### Shared code

The renderer imports the phone client's sources from `pkg/remote/web/src`
through the `@web` alias: `protocol.ts` (wire types and guards), `rpc.ts`,
`controller.ts`, `state.ts`, `markdown.ts`, `diff.ts`, `theme.ts` and a few
small helpers. They are not copied. `bun test` in `pkg/remote/web` checks
the guards against the fixtures in `pkg/remote/wire/testdata`. A protocol
change that breaks the desktop fails `npm run lint` here. Main loads the
gRPC service from `proto/deck/v1/deck.proto` at run time; the build copies
the file next to it.

## Security

For both transports:

- **No token in the window.** The renderer never sees a token: not the v2
  device token, not the rpc launch token. Main makes every connection and
  every call.
- **No Node in the window.** `contextIsolation`, `sandbox` and no
  `nodeIntegration`. The preload exposes a fixed list of methods. Main
  checks the type of each argument; an rpc call must name a known unary
  method of `deck.v1` and pass a plain object.
- **CSP.** The page loads from `app://deck` with `default-src 'none'`,
  `script-src 'self'`, `style-src 'self'` and `connect-src 'none'`: the
  window cannot open a network connection of its own. Main sends the policy
  as a header, and the built page carries it as a meta tag.
- **Safe rendering.** Agent text becomes a node tree (the shared
  `markdown.ts`), never HTML. A test bans `innerHTML` and its relatives in
  `src/`. Links open in the system browser, and only http(s) links are
  links.
- **Locked window.** The window cannot navigate away or open a new window.
  Permission requests (camera, notifications and others) are denied.

Open a folder (rpc):

- Deck listens on a Unix socket that only this user can open (mode 0600 in
  a 0700 folder). There is no TCP port.
- The token is made for one launch, printed once on the stdout pipe that
  only the app reads, kept in main's memory and never stored. Main sends it
  as `authorization: Bearer <token>` on each call.
- Deck lives as long as the app holds its stdin. Closing the folder or
  quitting the app closes stdin, and Deck stops.

Pair with a Deck (v2):

- **Token handling.** The renderer's `hello` carries a placeholder, and
  main writes the real token in. After the hello, main sends only `req`
  frames from the window. When Deck closes with 4401 `token_invalid` or
  4403 (revoked), main forgets the token.
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
