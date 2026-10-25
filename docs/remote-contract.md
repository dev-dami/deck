# Phone remote control, rebuilt: the contract

Status: v1, agreed 2026-10-12. Two builders work against this document in
parallel, one on the Go backend and one on the phone client. Neither reads
the other's code; this file and `pkg/remote/wire` (types and the fixtures in
`testdata/`) are the only shared surface. Anything not written here is the
builder's choice, within the rules of the repository.

Decided: layout `pkg/remote` (§2); v1 is the core, tunnels and push follow
(§9); pairing reuses the peers primitives (§1 R5, §3.5); LAN mode is
self-signed HTTPS (§1 R9); the command is `deck remote` (§1 R5).

## 0. What #47 got right and keeps

- The two-tool model stays untouched: the phone talks to the workspace, never
  to agents directly, and never gains an op agents do not have.
- The Go server design: one JSON frame envelope over a WebSocket, a hub with
  an epoch and a replay ring, a `Backend` interface so the server is tested
  against a fake. Kept as the shape; rebuilt, not copied.
- The phone client's rendering: Markdown to a node tree, never HTML; the
  `innerhtml` lint stays and is part of CI. Preact, Bun, biome stay.
- Pairing by one-time code in a QR, per-device keys stored as hashes,
  revocation that drops a live phone within seconds.

## 1. What was wrong and the rule that fixes it

Each rule below is the fix for a finding in the #47 review, verified on the
branch. Builders must satisfy every rule; the tests named in §7 check them.

| # | Finding on #47 | Rule for the rebuild |
|---|---|---|
| R1 | A global limiter disables all auth after 20 failures from anyone; the server then answers 4401 and the client wipes its token. Twenty `POST /api/v1/pair` from any web page on the desktop log out every phone. | No global lockout. Limits are per source address and per device. A rate-limited or locked-out connection gets its own error (`rate_limited`, close 4429) that the client treats as "wait and retry", never as "forget the token". The client forgets a token only on `revoked` (4403) or `token_invalid` (4401 with that code), both of which the server sends only after a constant-time check of that token. The limiter is consulted *after* a token lookup fails, so a valid token is never blocked by someone else's failures; the per-source lockout is `internal/deck/peer`'s (`lockout.go`), not a second one. |
| R2 | `cloudflared` is started with a context that is cancelled right after start, so the tunnel dies. | A tunnel's process lives exactly as long as the server: it is started with the server's context, its exit is reported as a server event, and `Close` is the only other thing that stops it. A test starts a fake tunnel program and checks it is alive after the server is up and dead after `Close`. |
| R3 | Several pairing codes can be live at once; a phone pairs silently; one token works for every project and never expires. | One code at a time, armed by the local person, shown in the TUI or CLI, 5 minutes, replaced by the next; a successful pairing is announced where the code was shown (device name and id), as a `device.paired` event to the TUI and every phone and a line on the `deck remote` console. Device keys are scoped to the install that issued them (`server_id`, random per data dir) and to one project (data dir); a key presented to another project is `token_invalid`. Keys expire after 90 days of no use. |
| R4 | 12 MiB may be read before the hello is checked; no cap on connections; the per-address map grows forever. | Before the hello is accepted: read limit 4 KiB, 5-second deadline, at most 4 unauthenticated connections per source and 32 in total, the rest refused at upgrade. After the hello: 12 MiB. Limiter state is pruned on a timer. |
| R5 | `deck serve`, the device store and the ports collide with the peers feature (`docs/peers-design.md` §8 phase 7 reserves `deck serve` for the headless engine; peers own `~/.deck/peers/`). | The command is `deck remote` (subcommands `pair`, `devices`, `revoke`); the TUI flag is `--remote`. State lives in `<data dir>/remote/` (devices, VAPID keys, push subscriptions), never under `~/.deck/peers/`. Nothing here opens port 7722. The pairing code comes from `peer.NewPairCode`, the per-source lockout is `peer`'s, and the line a person can type instead of scanning is a `peer.Invite` (`deck://pair?v=1&host&port&code&fp&name&exp`, parsed by the page). |
| R6 | The phone's palette is hard-coded and ignores the TUI theme. | `welcome.server.theme` carries the resolved palette (from `internal/ui/styles` `ExportResolvedPalette`, plus `tone.Tint`/`tone.Mark` per agent kind). The client sets CSS variables from it and has no agent colour of its own; a `theme.changed` event updates it live. |
| R7 | The pair route has no Origin or Content-Type check. | `POST /api/v1/pair` requires `Content-Type: application/json`, an `Origin` equal to the page's own origin, and `Sec-Fetch-Site: same-origin` when the browser sends it. |
| R8 | Behind `tailscale serve` every phone is 127.0.0.1; behind cloudflared any local process can spoof `CF-Connecting-IP`. | The client address used for limits is the socket address, except that a tunnel mode declares the one header it trusts and only for connections from loopback; when the mode gives every client the same address (tailscale), per-device limits carry the load and the per-address limit is widened to the device count. |
| R9 | `--lan` is plain HTTP: the code and token cross the Wi-Fi in clear. | `--lan` serves HTTPS with a self-signed certificate made once per data dir (`<data dir>/remote/tls.{crt,key}`, 0600, ECDSA P-256, SANs = the LAN addresses, 1 year). Its SHA-256 fingerprint is in the QR and the invite (`fp=`). A page cannot pin a certificate, so the page shows that fingerprint and asks the person to compare it with the one the browser warns about before they accept; the code is never sent until they do. Plain HTTP is loopback only; there is no `--insecure`. |
| R10 | Any paired device can revoke any other; queue and approval ids are not checked against the session. | A device may revoke itself; revoking others is the CLI's and the TUI's. Every id in a request (approval, queued item, message) must belong to the session named in the request, else `not_found`. |
| R11 | `device.revoked` is written without a seq and never decodes; focus is lost on reconnect; `messages.list` drops `more`; `hello.v` is not checked; internal error text and `cwd` reach the phone; a failed send is remembered as sent; the idempotency map is unbounded. | Fixed in the protocol below: every event has a seq; `hello.resume` carries `focus`; `messages.list` returns `{messages, more}`; `hello.v` must be 2 or the server closes 4400; errors carry a code and a short message, never a Go error string; `welcome` carries the project name, never a path; an idempotency key is recorded only after the send succeeded, in a map capped at 256 keys with a 10-minute TTL. |
| R12 | Outbound text is unbounded; `messages.list` loads the whole session per page; replay can kick a slow phone. | Every string the server sends is capped (text 64 KiB, reasoning 16 KiB, tool output 8 KiB, command 4 KiB, diff 256 KiB) with a `truncated` flag; message pages are read by cursor; replay is written through the same bounded queue as live events. |
| R13 | `tailscale serve --bg` overwrites the user's 443 config and `off` removes it. | Tailscale mode refuses to start when a `serve` config exists on the port, and restores nothing it did not set. |
| R14 | Push: endpoint port unchecked, redirects followed, stored endpoints not re-validated; approvals may stay in `waiting`. | Push endpoints are validated on subscribe and again on load (https, known host, port 443, no userinfo); the push HTTP client does not follow redirects; an answered approval leaves the waiting set at once. |
| R15 | `--allow-yolo` is a server-wide switch: any paired phone can then run anything. | Yolo is a per-device grant, given from the CLI or TUI (`deck remote devices --allow-yolo <id>`), shown in `devices.list`, off for every new device; `mode.set yolo` from a device without it is `yolo_disabled`. |
| R16 | `Verify` rewrites `devices.json` on every connect under a cross-process lock; a stuck lock logs everyone out; every server polls the file every 3 s. | `Verify` is read-only. Last-seen is written at most once a minute per device, off the connect path. One watcher per process notices revocations (file change, with a 3-second poll as the fallback), and a lock that cannot be taken fails that write, never a connect. |
| R17 | The WebSocket accepts an empty `Origin`. | The PWA is the only client in v2: the upgrade requires `Origin` equal to the page's own origin, and `/api/v1/pair` the same (R7); there is no header that bypasses it. |

## 2. Layout under `pkg/`

```
pkg/remote/            Go backend (the "desktop" side)
  wire/                frame and entity types, fixtures; no logic
  auth/                device store, pairing codes, limits
  hub/                 epoch, seq, replay ring, bounded per-client queues
  server/              HTTP + WebSocket, routes, headers, tunnels, push
  backend.go           the Backend interface and its sentinels
  adapter/             Backend over internal/workspace (the only file that imports it)
pkg/remote/web/        the phone client source (Bun + Preact), built into
  dist/                committed build, embedded by server
```

`internal/remote` and `web/remote` from #47 are not merged; the rebuild
replaces them. `internal/workspace/remote_caps.go` is rewritten as the
`RemoteControl` interface the adapter needs (§5). The empty `pkg/desktop` and
`pkg/mobile` directories are not used.

Module: `pkg/remote` is part of the main Go module. The phone client is its
own Bun project at `pkg/remote/web` (not a Go package); its build output
`pkg/remote/web/dist` is committed and embedded by `pkg/remote/server`.

## 3. Wire protocol v2

One JSON object per WebSocket text frame: `{"t": <type>, ...}` with only the
fields of that type. Numbers are JSON numbers; times are RFC 3339; binary is
base64 in a string. Unknown fields are ignored by both sides; unknown frame
types, methods and events are an error (`invalid`), never a crash.

### 3.1 Frames

| t | Fields | Direction |
|---|---|---|
| `hello` | `v: 2`, `token`, `resume?: {epoch, seq, focus?}` | phone → server, first frame, within 5 s |
| `welcome` | `epoch`, `replay: bool`, `server: {version, project, kinds[], allow_yolo, features[], server_id, protocol: 2, headless, tunnel: ""|"cloudflare"|"tailscale", push_key?, theme}`, `device: {id, name, expires_at}` | server → phone |
| `req` | `id: string`, `m`, `p?` | phone → server |
| `res` | `id`, `ok: bool`, `r?`, `err?: {code, msg}` | server → phone |
| `ev` | `seq: int`, `e`, `p` | server → phone |
| `resync` | — | server → phone: refetch everything, replay was impossible |

Error codes: `unauthorized`, `token_invalid`, `rate_limited`, `forbidden`,
`not_found`, `invalid`, `yolo_disabled`, `busy`, `too_large`, `internal`.
`msg` is at most 200 characters of text meant for the person, never a Go error.

Close codes: 4400 bad hello, 4401 token invalid, 4403 revoked, 4408 slow
consumer, 4429 rate limited (retry after the `Retry-After`-style number of
seconds in the close reason).

### 3.2 Methods

Unchanged from #47 unless noted. Every method that names a session takes
`session_id` and every id in its params must belong to that session.

| Method | Params | Result |
|---|---|---|
| `sessions.list` | — | `Session[]` |
| `sessions.create` | `{title?}` | `Session` |
| `sessions.rename` | `{id, title}` | `{}` |
| `sessions.delete` | `{id}` | `{}` |
| `sessions.focus` | `{id}` (`""` unfocuses) | `{}` |
| `messages.list` | `{session_id, before?: cursor, limit?: ≤100}` | `{messages: Message[], more: bool, cursor?}` |
| `chat.send` | `{session_id, text, images?: [{name, mime, data_b64}], idempotency_key?}` | `{message_id}` |
| `message.edit` | `{session_id, message_id, text}` | `{}` |
| `chat.cancel` / `chat.continue` | `{session_id}` | `{}` |
| `agent.interrupt` | `{session_id, agent}` | `{}` |
| `queue.list` | `{session_id}` | `QueuedItem[]` |
| `queue.steer` / `queue.cancel` | `{session_id, item_id}` | `{}` |
| `roster.get` | `{session_id}` | `AgentLine[]` |
| `roster.kinds` | `{session_id}` | `{kind, model?, joined}[]`: installed agent CLIs; `joined` when the session has a live member of the kind |
| `roster.add` | `{session_id, kind, model?}` | `AgentLine`: starts a member without sending it anything; unknown kind is `invalid` |
| `models.list` | `{session_id, kind}` | `ModelChoice[]` |
| `models.set` | `{session_id, kind, model, effort?}` | `{}` |
| `mode.get` | `{session_id}` | `{mode}` |
| `mode.set` | `{session_id, mode}` | `{mode}` |
| `approvals.list` | `{session_id?}` | `Approval[]` |
| `approvals.answer` | `{session_id, id, decision: allow|allow_session|deny}` | `{resolved}` |
| `changes.get` | `{session_id}` | `ChangedFile[]` |
| `devices.list` | — | `Device[]` (the caller's device is marked `self`) |
| `devices.revoke` | `{id}` | `{}` (only `self` from a phone) |
| `push.status` | — | `{available, enabled, approvals, done}` |
| `push.subscribe` | `{subscription: {endpoint, keys: {p256dh, auth}}, approvals, done}` | `{}` |
| `push.unsubscribe` / `push.test` | — | `{}` |

Mode is per session in v2 (the TUI's modes are per thread); `mode.get` without
a session is `invalid`.

### 3.3 Events

Every event has a `seq`. Scoped events go only to phones focused on that
session and are not replayed; a phone refetches the session on focus.

| Event | Payload | Scoped |
|---|---|---|
| `session.created` / `session.updated` / `session.deleted` | `Session` | no |
| `message.created` / `message.updated` | `Message` | yes |
| `message.deleted` | `{id, session_id}` | yes |
| `approval.requested` | `Approval` | no |
| `approval.resolved` | `{id, session_id, granted}` | no |
| `busy` | `{session_id, busy}` | no |
| `queue.changed` | `{session_id, items}` | yes |
| `roster.changed` | `{session_id}` | no |
| `mode.changed` | `{session_id, mode}` | no |
| `notice` | `{session_id, text}` | yes |
| `theme.changed` | `theme` | no |
| `device.paired` | `Device` | no |
| `device.revoked` | `{id}` | no (followed by close 4403 for that device) |

The hub's epoch, replay ring and cursors live in memory: after a server
restart every phone gets `resync` and refetches. That is by design; nothing
in the replay path is persisted.

### 3.4 Entities

As in #47's `wire.go` with these changes: `Message.parts[].image.name` is a
display name, never a path; `Approval.agent` is the agent id; every long
string field has a sibling `truncated: bool` when cut; `Device` gains
`expires_at` and `self`; `Session` gains `mode`. The Go types in
`pkg/remote/wire` and the TypeScript guards in `pkg/remote/web/src/protocol.ts`
are both generated-by-hand from this section and checked against the shared
fixtures in `pkg/remote/wire/testdata/*.json` (one file per frame, entity and
event; both sides must decode every fixture and encode it back byte-equal
after key sorting).

### 3.7 Encoding (fixed by `pkg/remote/wire` and its fixtures)

- `Frame` is flat for `id`, `m`, `ok` (`*bool`), `r`, `err`, `p` (shared by
  `req` and `ev`); the `hello`, `welcome` and `ev` groups are written whole,
  so `v`, `replay` and `seq` appear even when zero.
- Every timestamp is an RFC 3339 string; `Device.last_seen` is absent when
  the device was never seen.
- A capped string's flag is `<field>_truncated` (`text_truncated`,
  `output_truncated`, `diff_truncated`, `command_truncated`,
  `raw_truncated`); `raw` is capped at 4 KiB like `command`.
- An image part is `{type: "image", image: {name, mime}}`; the same `Image`
  with `data_b64` is the `chat.send` upload. v1 has no way to fetch an
  image's bytes back to the phone.
- `Approval` has no `cwd`; `path` is relative. `AgentLine` has `id`, which is
  what `Approval.agent` and `agent.interrupt.agent` name. `Device` has
  `allow_yolo`.
- `theme` is `{palette: {token: "#rrggbb"}, kinds: {kind: {tint, mark}}}`.
- `messages.list` cursors (`before`, `cursor`) are opaque strings.
- Methods without params or result use `{}`. `sessions.delete`,
  `sessions.focus` and `devices.revoke` take `{id}`; `chat.cancel`,
  `chat.continue`, `queue.list`, `roster.get`, `roster.kinds`, `mode.get` and
  `changes.get`
  take `{session_id}`.
- The pair route's error body is `{error: code}`. `welcome.server.features`
  and `kinds` are always present (possibly empty).
- Fixture names: `frame_<t>`, `entity_<name>`, `event_<name>`,
  `req_<method>`, `res_<method>`, `http_<name>`; sorted keys, 2-space
  indent, trailing newline. The Go test fails on a missing, unregistered or
  non-canonical fixture; the TypeScript test must do the same.

### 3.5 Pairing (HTTP)

- Routes: `GET /api/v1/health`, `POST /api/v1/pair`, `GET /api/v1/ws` (the
  WebSocket), everything else the static page. The fingerprint in `fp=` and
  in the server advert is `SHA256:<base64, 43 chars, no padding>` of the
  DER certificate; the page converts it to colon-separated hex pairs, which
  is how browsers show it, and `deck remote` prints the hex form.
- `welcome.server.features` in v1 is `["images", "edit", "changes", "queue",
  "models", "devices"]`. `messages.list` defaults to 50 when `limit` is
  absent. A `chat.send` image over 8 MiB is `too_large`. `devices.revoke` of
  another device from a phone is `forbidden`; revoking itself answers `{}`
  and then closes 4403.
- Pair route statuses: 403 `forbidden` (origin or `Sec-Fetch-Site`), 415
  `invalid` (content type), 400 `invalid` or `unauthorized` (bad or wrong
  code), 429 `rate_limited` with `Retry-After`, 500 `internal`. The pair
  lockout is consulted *before* the code is checked: the code is the secret
  there, so brute force must be slowed; the WebSocket token path consults it
  after the token lookup, as R1 says.
- `GET /api/v1/health` → `{ok: true, protocol: 2}`; no auth, no other field.
- `POST /api/v1/pair` with `{code, device_name}` (≤ 4 KiB, rules R7) →
  `{token, device_id, server_id, expires_at}` or `{error: code}` with 400/429.
  The code travels in the QR URL's fragment (`#pair=…`) and is removed from
  history by the page before anything else runs.
- The token is sent only in `hello`; it is never in a URL, header or cookie.
  The phone stores it in `localStorage` as `deck.remote.token.<server_id>`,
  with `deck.remote.server` naming the install last paired at this origin
  (the page learns `server_id` from the pair response, not from `health`).
  A `token_invalid` or 4403 clears that install's token only.
- An invite that names another host:port is not pasted cross-origin: the page
  shows a link to `https://host:port/#pair=<code>&fp=<fp>` instead (R7).
- Close reasons: 4401 always carries the reason `token_invalid`; 4429 carries
  a bare integer, the seconds to wait. The client forgets a token only on
  4403 or a 4401 whose reason starts with `token_invalid`; every other 4401
  and every 4429 backs off (the number as a floor, jittered, capped at 60 s)
  and keeps the token.

### 3.6 Headers and limits (server)

CSP `default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'
data: blob:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none';
form-action 'none'`, `X-Content-Type-Options: nosniff`, `Referrer-Policy:
no-referrer`, `Cache-Control: no-store` on `/api`. Host allow-list as in #47.
Per connection: 8 requests in flight, 2-minute call timeout, 75-second idle
timeout, 30-second ping, 256-frame outbound queue (4408 when full).

## 4. Backend interface (Go)

The server depends only on this; the adapter implements it over the
workspace. Signatures as in #47's `backend.go` with these changes: `Mode`,
`SetMode`, `Approvals`, `Answer`, `Steer`, `CancelQueued` take a session id
and return `ErrNotFound` for ids outside it; `Queue`, `Roster` and
`Approvals` return an error too, so an unknown session is `not_found`
(`Approvals("")` is every session's, for `approvals.list` without a
`session_id`); a `Verify` error that is not a bad, expired or
out-of-scope token (an I/O error, a contended lock) is never `token_invalid`:
the socket closes 1011 and the phone keeps its token;
`Send` returns the new message's id; `Messages` takes a cursor and limit and
returns `(msgs, more, next, err)` (v1 pages in memory with an opaque
`created:id` cursor; a real database cursor is a follow-up); `Info` returns
`Project` (a name) and `Theme`, never `Cwd`; `Events` also yields
`theme.changed` (v1 notices a theme change by comparing `Theme()` every 5 s,
there being no config-change event).

Pairing works across processes through files in `<data dir>/remote/`:
`pair.json` (the armed code's hash and expiry) and `server.json` (the running
server's advert: url, hosts, port, tls, fingerprint, pid), so `deck remote
pair` works beside `deck remote` or `deck --remote`. The invite line is
printed only over `--lan` (an invite needs `fp=`); loopback prints the URL.
`deck remote devices` takes `--allow-yolo <id>` and `--deny-yolo <id>`. An
oversized hello may be closed with 1009 by the WebSocket library before the
server's 4400; the client treats both as a protocol problem.

## 5. Workspace boundary (Go)

`internal/workspace.RemoteControl` is the one interface the adapter needs
from `DeckWorkspace`: `RemoteMode(sid)`, `SetRemoteMode(sid, mode)`,
`QueueItems(sid)`, `SteerQueued(sid, item)`, `CancelQueued(sid, item)`,
`InterruptAgent(sid, agent)`, `ContinueThread(sid)`, `ChangedFiles(ctx, sid)`,
`EditableMessage(sid, mid)`, `EditMessage(ctx, sid, mid, text)`,
`SubscribeDeck(ctx) <-chan pubsub.Event[DeckChange]`, `Theme() Theme`, and
`PostMessage(ctx, sid, text, images) (messageID, error)` (an `AgentRun` that
returns the row it made). `DeckWorkspace` publishes the busy, queue, mode,
roster and notice changes from its existing hooks (small additions in
`deck_workspace.go`). `internal/deck/peer/shared.go` exports the lockout and
private-file helpers the phone's `auth` package reuses (`Lockout`,
`LockKey`, `RestrictPrivate`, `WritePrivateFile`). Those three places are
the whole of the rebuild's footprint in `internal/`.

## 6. Client contract (phone)

- Connects, sends `hello`, and treats 4429 as back off (jittered, capped at
  60 s) with the token kept; treats 4401 `token_invalid` and 4403 as forget
  the token and show pairing; treats `resync` and a fresh epoch as refetch.
- Sends `hello.resume.focus` only while a session is open; after a resumed
  reconnect refetches that session's messages, queue and roster; after a new
  epoch or `resync` refetches everything and sends `sessions.focus` again.
- `chat.send` is retried only on timeout or disconnect, up to 3 times, with
  the one idempotency key minted for that message. `messages.list` is asked
  with `limit: 100` and `before: <cursor>`.
- Decodes a Go `null` slice or map as empty and a `{}` result with no `r` as
  empty; `Session.mode` is accepted as any string so an unexpected mode never
  drops a session, while `mode.*` is strict.
- Renders nothing as HTML (the lint stays); keeps only `http(s)` links;
  opens them with `rel="noopener noreferrer"`.
- Takes every colour from `welcome.server.theme`; has no palette of its own
  beyond neutral-grey fallbacks used only before `welcome`. The launcher
  icons are the brand mark, not the palette. The page's meta CSP mirrors
  §3.6 (less `frame-ancestors`, which only a header can set).
- Pairing over https with a fingerprint shows it as SHA-256 colon-hex and
  gates the code behind a checkbox (R9); https without one gates behind a
  plain "I trust this connection"; plain http (loopback) pairs directly.
- Validates every frame, entity and event at runtime against §3 and drops
  what fails with a console warning, never a crash.

## 7. Tests each side must ship

Backend: wire fixtures round-trip; auth store (scope, expiry, revoke, Windows
ACL); limiter (per-address, per-device, pruning, never global); server with a
real WebSocket client against a fake Backend (R1, R4, R7, R10, R11, R12 each
as a named test); tunnels with fake programs (R2, R13); push with a fake
sender (R14); `deck remote` command flags. No test may need a network beyond
loopback or a real tunnel program.

Client: protocol guards against the same fixtures; reducer; rpc reconnect and
close-code handling (R1); controller focus-on-reconnect; markdown; the
innerhtml lint; build. `bun test` and `bun run build` must pass in CI and the
committed `dist` must match the build.

## 8. Scope of v1

In: pairing (QR, invite line, `deck remote pair`), sessions, chat with
images, message edit, approvals, queue steer/cancel, interrupt/continue,
modes, models, changes, devices (list, revoke self), theme, loopback and
`--lan` (self-signed HTTPS). The `push.*` methods and the `tunnel` field
exist in the protocol from the start; in v1 `push.status` answers
`{available: false}` and `tunnel` is `""`.

Out, as follow-up PRs on this contract: cloudflared and tailscale tunnels
(R2, R8, R13), push (R14), the TUI Remote Control dialog beyond showing the
pairing code and the paired devices.

## 9. PR plan

1. `pkg/remote/wire` + fixtures + this document (the contract, mergeable on
   its own).
2. `pkg/remote/auth`, `hub`, `server` with the fake Backend and the R-tests.
3. `pkg/remote/adapter` + `internal/workspace` `RemoteControl` + `deck remote`.
4. `pkg/remote/web` + embedded dist + CI job.
5. Tunnels and push (each its own PR; both opt-in).

#47 is closed when PR 3 lands, with a note pointing here.

## 10. gRPC transport (desktop app)

The Deck desktop app does not use the WebSocket above. It runs
`deck serve --rpc -c <project>` and speaks gRPC, defined in
`proto/deck/v1/deck.proto` (Go code in `pkg/rpc/deckv1`, regenerated with
`buf generate`). The server, `pkg/rpc`, is a thin layer over the same
`remote.Backend` (§4), so both transports drive the same adapter.

- **Transport.** A Unix socket under `<data dir>/run/`, or under the temp
  dir if that path is too long. It works on Windows 10 1803+ too, and there
  is no TCP port. The socket is mode 0600 inside a 0700 directory.
- **Handshake.** Once listening, the CLI prints one JSON line on stdout:
  `{"v":1,"sock":…,"token":…,"pid":…,"version":…}`. Every call carries
  `authorization: Bearer <token>`. The token is minted per launch and never
  stored. The process exits when its stdin closes.
- **Events.** `Events(Subscribe{epoch, seq})` opens with `Ready`, replays
  what was missed from a 4000-event ring, then streams live. `Ready.resync`
  means refetch. A subscriber that falls 2048 events behind is ended with
  `RESOURCE_EXHAUSTED` and resumes from its last seq. Nothing is scoped to
  a focused session; the desktop gets every event.
- **Differences from the phone.**
  - Images are raw `bytes`, up to 16 MB each.
  - Text is not capped.
  - Yolo is allowed, because the caller is the user at this machine.
  - Errors carry their message.
  - Rooms. `Session` carries `attention`, `branch` and `archived`,
    `Notice.via` marks overlap notes, and `lease_changed` reports a lease
    taken or ended. The phone gets none of these.
- **Optional calls.** `Info.capabilities` names the optional methods this
  Deck serves: the room calls (isolate, apply, unisolate, revert, undo,
  archive, race) and `ListFiles`. A client checks it before it calls one.
  `Info.clis` lists the agent CLIs Deck looks for and where it found them.
