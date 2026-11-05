# 02. What v3 deletes

Status: plan, 2026-11-05. Repo at `eec5677`. Paths and line numbers were
checked against that commit; line counts are `wc -l` and include tests unless
a row says otherwise. Decisions D1–D19 are in [00-overview.md](00-overview.md).

v3 is a smaller product before it is a bigger one. This chapter lists what
goes, why, what (if anything) takes its place, and in what order, so that
nothing is deleted before its replacement exists and nothing we need is
deleted by accident.

Three rules govern every cut:

1. **A cut names its replacement, or says plainly that there is none.**
2. **Nothing on the keep list (§6) is touched**, even when it sits in a file
   that is being deleted. It is moved first.
3. **Cuts that remove access land with the thing that restores it.** The peer
   link and shared threads are deleted in the same release that ships Deck
   Link roles (§5).

Roughly **115–120k lines** go, tests included:

| Group | Approx. lines | Of which Go |
|---|---|---|
| Team connect (engine, proto, TUI, desktop, docs) | ~33,000 | ~30,500 |
| Duplicate connection systems (beyond the peer link) | ~3,500 | ~3,000 |
| Duplicate state (stores, converters, caches, attention, polling) | ~9,000 | ~8,000 |
| Legacy concepts | ~1,500 | ~1,300 |
| Duplicate surfaces (outside desktop-shell) | ~800 | ~800 |
| Clients: desktop-shell at GPUI parity, the two PWAs | ~73,000 | 0 |

Phases refer to the order of work in 00-overview §5: **P1** foundation, **P2**
TUI on the protocol, **P3** GPUI desktop (desktop-shell is deleted at **M5**),
**P4** Deck Link, **P5** remaining cuts, **P6** phone.

---

## 1. Summary table

### 1.1 Team connect

The owner called team connect "trash". About 33k lines serve team chat, an
outbox, two kinds of contract and a bash sandbox, behind a pairing flow the
desktop cannot reach (`desktop-shell/src/renderer/ui/Team.tsx:380,409` point
to Settings controls that do not exist). The one part people want, direct
access to a host's threads, is the weakest part: a teammate sees a rendered
text mirror (`SharedMirror.lines`, `proto/deck/v1/deck.proto:1268`), grants
are keyed by a Deck name (`internal/deck/session/share.go:115-130`), and only
threads in memory are served (`share.go:116-118`).

| Item | Area | Why it goes | Replaced by | ~Lines | Phase |
|---|---|---|---|---|---|
| **Team chat**: `console.post`/`console.read`, `chat.jsonl`, the agents' `team.read`/`team.post` ops, team news in every briefing | engine, TUI, desktop | Messaging is out of scope (D8). It also costs tokens in every agent's first instructions even when no one uses it (`session/briefing.go:445`) | Nothing. People talk in the thread itself, through direct access | ~2,300 (`peer/team.go` 487 + test 202, `session/team.go` 209, `hub/team.go` 127, `engine/team.go` 312, TUI `pane_chat` 519, desktop share) | P1 (early) |
| **Outbox** (`outbox.json`, 200 calls, 7 days) | engine | Carried chat and contracts only. Each process loads it once and rewrites it whole with no cross-process lock, so two engines overwrite each other (`peer/outbox.go:50-60`) | The Deck Link offline mailbox (the existing v3 relay queue) for the queueable actions in `docs/mobile-contract.md` §6 | ~250 | P1 (early) |
| **Code contracts**: `task.contract`, `contract.propose/status/result/cancel`, `contracts.json`, tasks owned by `vps/claude` | engine, TUI, desktop | Remote work by message passing, with a lifecycle that needed several after-the-fact fixes (`a5ca742`, `19f0bcc`, `6075879`) | Direct access (a teammate works in the host's thread) and moving a thread to another host (D17) | ~2,700 (`peer/contract.go` 876 + test 343, `session/contract.go` 370 + test 210, `engine/contract.go` 228 + test 305, TUI/desktop cards) | P1 (early) |
| **Ops contracts and the vsh sandbox**: `sh.run`, `contract.result`, schema results, `audit.jsonl`, `peers.sandbox` | engine | A second remote-execution system with its own shell dialect, used only for teammates' server reports. `peers.sandbox.presets` is "parsed but not used" (`config/config.go:358-366`) | Nothing now. An admin opens a thread on that host. Scheduled host reports come back as Routines (D7, later) | ~4,300 (`peer/opscontract.go` 254, `opsparse.go` 111, `audit.go` 100, test 299; `internal/deck/vsh` 2,349; `session/opscontract.go` 573; `engine/ops.go` 140; the `ContractSafe`/`OpsSafe` adapter flags, 24 references) | P1 (early) |
| **Peer SSH server on :7722** with pairing (argon2 code, `deck://pair`), identity, pinning, supervisor, manager, control socket, MCP over the link, `peer.status` presence polling | engine | A fourth connection system with its own keys, its own pairing and a required open inbound port. Only one process gets the port and the socket (`docs/peers-design.md` §1) | Deck Link: Noise IK on every path (local socket, tailnet, direct, Noise relay, SSH stdio) (D9, D13, D14) | ~13,500 of `internal/deck/peer` (17,141 total, minus the parts listed in other rows and ~800 lines of helpers that move) | P4 |
| **Deck-as-relay** (`relay@deck`, `relayed@deck`, `relay-pair@deck`) | engine | Needs a third Deck with a public port and three opt-ins on three Decks (`docs/peers-design.md` §1, commit `f3bcdc2`) | Tailscale (DERP) and the self-hostable Noise relay, `deck relay` (D14) | ~1,270 (`peer/relay.go` 572, tests 698) | P4 |
| **`deck peer`** (`pair/add/list/ping/remove/relay/fingerprint`) | CLI | Commands for the deleted link | `deck invite`, `deck host members` (chapter 05) | ~1,210 (`internal/cmd/peer.go` 283 + test 87, `peer/cli.go` 653 + test 187) | P4 |
| **The `peers` config section** and `deck doctor`'s peer checks | config, doctor | Configures the deleted link. Roles in it point the opposite way from share roles (`config.go:301-305` vs `session/share.go:31-35`) | The Deck Link member and device store (`pkg/remote/devstore`, grown into members with roles) | ~730 (`config/config.go:142-145, 296-500`, `config/peers_test.go` 226, `doctor/peers.go` 162 + test 137) | P4 |
| **Shared threads**: `thread.mirror/post/answer`, share roles observer/participant/editor, `Thread.Shares` | engine | A text mirror, not a room; grants keyed by name, so a re-pair under the same name inherits access (`multiplayer-design.md` §8.5) | Direct access with roles viewer/member/admin (D8, D15): the teammate opens the full thread over the same proto | ~900 (`session/share.go` 332 + test 115, `peer/share.go` 226 + test 59, `engine/share.go` 45 + test 88) | P4 |
| **`deck remote` v2** (HTTPS + WebSocket, wire v2) | CLI, pkg/remote, desktop | A second phone protocol and pairing flow; the desktop's "Pair with a Deck" rides on it and gets only "the open session's messages" (`desktop-shell/README.md:22`) | Deck Link pairing and the proto | ~2,500 (`internal/cmd/remote.go` 464 + test, the v2 parts of `pkg/remote/server` (3,002 total), `desktop-shell/src/main/pair.ts` 140, `pin.ts` 114) | P6 |
| **TUI Team, Team chat and Shared panes**, Settings › Team, the peer pairing wizard, the teammate parts of the Capsules pane | TUI | 3 of the 11 sidebar panes (plus Capsules' teammate sharing) are for a feature most users never set up, and they sit in the default columns (`internal/ui/model/pane.go:79-80`) | Host › Members in Settings (chapter 05); presence in the thread header | ~5,300 (`model/pane_team.go` 742 + test 110, `pane_chat.go` 217 + test 302, `pane_shared.go` 358, `dialog/settings_team.go` 1,033 + test 1,129, `dialog/peer_pair.go` 1,056, `internal/workspace/peers.go` 160, hooks in `ui.go` and `mentions.go` ~150) | P1 (chat, contracts), P4 (rest) |
| **Desktop Team page** and the shared room | desktop | Dead end: you cannot add a teammate from the desktop at all | Host › Members and direct access in the GPUI app | ~1,450 (`ui/Team.tsx` 596, `ui/SharedRoom.tsx` 264, the team half of `crew.ts`, `crewState.ts` team polling, "mate" lanes in `lanes.ts`, `SharedGroup` in `ProjectGroup.tsx`, `ShareStack`, the footer Team button, the contract inbox kind, `shared/bridge.ts:152-161`) | P1 (chat, contracts), P4 (rest) |
| **Team proto**: 9 RPCs, 18 messages, the team half of `pkg/rpc/team.go` | proto | Serve the features above | Deck Link member and presence calls (chapter 05) | ~2,100 (proto ~135, generated ~1,500, `pkg/rpc/team.go:279-455` + most of `team_test.go`) | P1 (chat, contracts), P4 (rest) |
| **Team connect docs** | docs | Describe deleted features, and are stale even for those (`peers-design.md` names files the code does not write) | `docs/link-contract.md` and `docs/fleet-design.md` (chapter 05) | ~700 | P4 |

### 1.2 Duplicate connection systems

Today there are four connection systems, three pairing flows, two relays and
four stores of keys and devices (research-team §3). v3 has one: Deck Link,
built on the phone's v3 stack (D13).

| Item | Area | Why it goes | Replaced by | ~Lines | Phase |
|---|---|---|---|---|---|
| Peer link | engine | See §1.1 | Deck Link | (in §1.1) | P4 |
| `deck remote` v2 and the desktop's "Pair with a Deck" (`servers.json`) | CLI, desktop | See §1.1 | Deck Link; desktop "Add a host" (D16) | (in §1.1) | P6 |
| **`deck serve --rpc` per project folder**, with a per-launch bearer token, dying with the desktop's stdin | engine, desktop | One full engine per open folder (`desktop-shell/src/main/decks.ts:249-252,410`; `internal/cmd/serve.go:270-330`); closing the app stops the agents (`serve.go:325-329`) | The local-socket path of the one host daemon (D3) | rework, ~200 removed | P1 |
| **`deck serve` headless mode** that refuses to start unless peers are on (`serve.go:201-208`) | CLI | Its only job was serving the peer link | `deck host run`. `deck serve` stays one release as an alias that prints a deprecation line | ~150 | P4 |
| **`deck mobile setup/daemon/pair/status/devices`** | CLI | Not deleted as code: the daemon is the base of Deck Link. The command name goes | `deck host` and `deck invite` (rename) | ~100 | P4 |
| **The wrangler-managed local relay** (`internal/mobilecontrol/managed.go`, `docs/mobile-lan.md`: a Cloudflare Worker run under Node on your machine) | engine | Asks for Node and wrangler before a phone works on a LAN | `deck relay`, a Go server speaking the same `relay/v1` lanes, self-hosted (usually on your VPS). The hosted Cloudflare relay (`pkg/remote/relay`) stays as an optional convenience | ~450 | P4 |
| **Three pairing flows**: peer argon2 code + `deck://pair`; v2 code in QR + `POST /api/v1/pair`; v3 IKpsk2 | all | Three vocabularies, three codes, three stores | One invite: the v3 IKpsk2 slot (5 minutes, burned on failure), extended with role and project | (counted above) | P4 |
| **Four key and device stores**: `~/.deck/peers/`, `<data dir>/remote/`, the mobile dir, desktop `servers.json` | all | Same | `pkg/remote/identity` (one host key) and `devstore` (members and devices) | (counted above) | P4–P6 |
| The never-built `tunnel` field (`cloudflare`/`tailscale`) in wire v2 | wire | Dead field (`docs/remote-contract.md` §8 lists tunnels as out of scope) | Tailnet paths in Deck Link | ~10 | P6 |

### 1.3 Duplicate state

The root cause of most UI bugs (chapter 01) is that state exists several times
and each copy is kept in step by hand.

| Item | Area | Why it goes | Replaced by | ~Lines | Phase |
|---|---|---|---|---|---|
| **The three stores for one conversation**: the engine log (`~/.deck/threads`), Crush's SQLite `sessions`/`messages` per project, and `deck-threads.json` mapping one to the other (`internal/workspace/deck_workspace.go:126`), plus the workspace entry index (`deck_workspace.go:85-90,121-133`) | engine, TUI | Clients read a lossy re-rendering of the engine log, with one-second timestamps and random-UUID tie-breaks (`pkg/remote/adapter/adapter.go:228-233`), so clients disagree on message order | One engine log per thread with durable revisions (D4) | ~3,800 (`internal/message` 1,409, `internal/session` 442, the `sessions`, `messages`, `files` and `read_files` queries in `internal/db`, the mapping and index code). The `usage` tables stay (§6) | P1 |
| **The Crush event path under the TUI**: `internal/app` fan-in, the lossy `internal/pubsub` broker, the single-slot Crush permission and question services | TUI | Lossy delivery (`pubsub/broker.go:174-196`), one global approval slot for every thread (`internal/permission/permission.go:207-208`), a question busy-poll (`internal/question/question.go:246-271`) | Typed per-thread streams from the daemon; approvals and questions keyed by thread | ~1,500 | P2 |
| **Three attention computations** (four counting the desktop's): `pkg/rpc/room.go:91-127`, the phone daemon's `inboxFor` (`pkg/remote/daemon/daemon.go:232-262`), the TUI's own from `workspace` calls, and `desktop-shell/src/renderer/attention.ts` | engine, clients | The phone and the desktop can disagree about whether a thread needs you; the phone ignores errors and blocked tasks | Engine-owned attention in the per-host summary stream (D4, D7) | ~300 | P1 (engine), P2/P3/P6 (clients) |
| **Two wire schemas and their hand-written converters**: `pkg/remote/wire` (the JSON mirror), `pkg/rpc/convert.go`, `pkg/remote/adapter/convert.go`, and `pkg/rpc` sitting on `remote.Backend` (`pkg/rpc/server.go:1-7`), itself an adapter over the Crush workspace | engine | Two lossy hops. `toModels` drops defaults and effort descriptions (`adapter/convert.go:215-227`); `toRoster` collapses status to a bool (`convert.go:198-205`) | `proto/deck/v1` as the only schema; the gRPC server sits on the engine. The sealed offline envelopes (queued actions, snapshots) stay and carry proto messages | ~4,000 (`wire` 2,339, `convert.go` 145 + 265, most of the rest of `pkg/remote/adapter`) | P1 (desktop/TUI path), P6 (wire deleted with the phone move) |
| **Three model caches and two picker APIs**: the adapters' `modelcache.Cache` (`internal/deck/agent/modelcache`), `Manager.modelCache` (`session/modes.go:231-257`), `DeckWorkspace.models` (`internal/workspace/agent_picker.go:73-100`); plus the desktop's own `agentCatalog.ts` and the Profiles editor's separate fetch | engine, clients | A slow first answer can pin the static list for the Manager's life; the picker asks every CLI before filtering to one | One catalog per host, one `ListCatalog` RPC, a `catalog_changed` event (D5) | ~250 | P1 |
| **Whole-config saves from several engines** (`internal/workspace/peers.go:151-160`, `agent_picker.go:149-171`) | engine | Project A's engine silently reverts what project B saved | One daemon per host; `SetDefaults` writes one key | ~80 | P1 |
| **Polling where events belong**: desktop `crewState.ts` (3 s/10 s), `workState.ts`, `limitsState.ts`, Team (30 s), Git (15 s); TUI lead re-read every 2 s (`internal/ui/model/lead.go:23-26`) and the 500 ms busy probe | clients | Stale data shown as fresh (`crewState.ts:50-60`); engine already has the events but turns them into telemetry (`deck_workspace.go:555-580`) | Board, agents, limits and lead events (D4) | ~400 outside desktop-shell | P1 (events), P2 (TUI) |

### 1.4 Legacy concepts

| Item | Area | Why it goes | Replaced by | ~Lines | Phase |
|---|---|---|---|---|---|
| **`@`/`$` addressing leftovers in the clients**: TUI help still advertises `@ agents & people ($ too)` (`internal/ui/model/keys.go:230`); `openAgentCompletions` has no callers (`ui.go:4568`); the old `$` model-switch path (`ui.go:4626-4645`); `peopleItems` teammate completions (`mentions.go:159-199`); desktop and phone `applyAgentChip` (`pkg/remote/web/src/composer.ts:7`); the phone's `$name` agent rail (`apps/mobile/src/screens/ChatScreen.tsx:321`) | TUI, desktop, phone | The sigil model changed four times in ten days (`107d9ce`, `2e7ea83`, `6faf490`, `22d77d9`). User posts now go to the lead and `@`/`$` are plain text (`session/manager.go:846-853`), so these controls do nothing | The lead line and the picker (D5); "Hand to…" by profile (D6) | ~250 | P2 (TUI), P3 (desktop), P6 (phone) |
| **Engine mention routing**: `session/address.go` (305 lines, used only by `postAddressed` for capsule hand-overs, `capsulework.go:566`); the Manager's package doc that still describes `@name`, `@all`, "the member who spoke last" (`manager.go:15-35`); kind and `claude*` pool targets on `thread.send`; `thread.freeName` renames (`claude` → `claude1`, `thread.go:468`); the `lastSpeaker` fallback (`manager.go:1469`) | engine | Unstable member ids break client keys; routing by kind picks "whichever is free" instead of the agent you meant | Delegation by agent id or profile as a structured task (D6); stable member ids, with display names kept apart | ~450 | P1 |
| **Three meanings of "contract"**: (a) hand-off terms (`store.Contract`, `store/task.go:194`; `session/handcontract.go`, `evidence.go`); (b) the peer code contract; (c) the ops contract | engine, docs | One word for three things, two of which are being deleted | (a) is **kept and renamed** to `store.Terms` (`session/terms.go`); the JSON key `contract` is still read for old threads. (b) and (c) are deleted (§1.1). Op descriptions say "with terms", not "under a contract" (`hub/hub.go:259`) | rename only | P1 |
| **Legacy multi-member APIs**: `roster.add`/`AddAgent` on the phone wire (`pkg/remote/server/rpc.go:199-207`, `pkg/remote/backend.go:85-88`); the flat `AgentKind/Name/Model/SessionID` mirror of `Members[0]` (`store/store.go:213-217`); `Thread.Orchestrate` bool (`store.go:197`); `StartRaceRequest.kind`/`count` (`deck.proto:1301-1308`); `SetOrchestrateRequest.on` and `BoardResponse.orchestrate` (`deck.proto:1067-1095`); `SetModel` itself; per-method `Info.capabilities` probing (`deck.proto:415-417`) | engine, proto, wire | Compatibility for clients that no longer exist; `SetModel` switches the lead, changes the model and rewrites the global config in one call (`agent_picker.go:149-172`) | `SetLead` / `SetMemberSpec` / `SetDefaults` (D5); race entrants; the orchestration level string; versioned features on the host info | ~300 | P1 (proto), P6 (phone wire) |
| **Crush model and Thinking leftovers in the TUI**: `restoreModelFromSession` writes Crush's global preferred model on every session load (`ui.go:1007, 2629-2690`); `ActionToggleThinking` flips a flag no Deck agent reads and reports "Thinking mode enabled" (`ui.go:2324-2345`); `currentModelSupportsImages` falls back to Crush's coder model (`ui.go:2906, 2914, 3730`); `UpdateAgentModel` is a no-op (`deck_workspace.go:433`); renderers for removed Crush LSP tools (`internal/ui/chat/references.go`, `rename.go`, `symbols.go`, `definition.go`, `call_hierarchy.go`, `lsp_restart.go`, `diagnostics.go`); the `"TODO: Implement Tool Renderer"` fallback (`chat/tools.go:97-100`); "Allow for Session" (`dialog/permissions.go:795`) | TUI | They lie to the user or write config behind their back | Image support from the catalog entry of the resolved spec (D5); generic renderers for Deck ops; Deck wording ("this thread") | ~800 (439 in the LSP renderers alone) | P2 |
| **Prose hand-offs and the regexes that read them back**: the relay written as `"→ $codex $claude · sub-thread <sid>"` (`deck_workspace.go:503-517`), `task t3 "…" done:` reports (`session/handoff.go`), parsed by `pkg/remote/web/src/handoff.ts` (86) and `internal/ui/chat/cards.go:37-70` | engine, clients | Any wording change breaks cards in two clients; an agent reply that starts with `→ $name` renders as a hand-off | Structured hand-off, task-report, plan and turn-end events (D4) | ~250 | P1 (events), P2/P3/P6 (parsers) |

### 1.5 Duplicate surfaces

Most of these live in desktop-shell and die with it at M5. The point of
listing them is that **the GPUI app, the TUI and the phone must not rebuild
them**.

| Item | Area | Why it goes | Replaced by | ~Lines | Phase |
|---|---|---|---|---|---|
| **Three "needs me" lists**: desktop Inbox page, notification bell centre (`ui/NotificationBell.tsx`, `notifyCentre.ts`, its own read state) and Highlights › Needs you (`ui/HighlightsPanel.tsx:21,84,135`); the TUI's global approval modals and Notifications history; the phone's Needs you tab over `inbox.get` | all clients | Three lists with three read states; the TUI's modal from another thread does not even name it (`ui.go:1140-1150`) | **Inbox + Now** (D7): Needs you, Review, Running. OS notifications and the badge feed from the same engine attention | TUI ~300; desktop dies at M5 | P2 (TUI), P3 (desktop), P6 (phone) |
| **About 11 delegation surfaces** on the desktop: lanes, timeline folds, hand-off cards, sidebar nesting, the Agents tab, the Graph room view, the Graph side tab, the History graph, Tasks, Work, notification-centre task entries, Highlights | desktop (reference for GPUI) | No single place shows what is running; Graph appears three times, agents four times | Four places: structured cards in the timeline; sub-thread nesting in the sidebar; **Now** (the live tree with Follow and Open, D18); the thread's right panel (Agents, Tasks, Work). The Graph tab, the History graph, the lane strip, the task entries in the bell and Highlights are not rebuilt | not ported | P3 |
| **Dead Steer buttons**: lane "Steer" and Graph detail "Steer" call `address()`, which only inserts `$name` into the draft (`desktop-shell/src/renderer/ui/Room.tsx:302-305`; `ui/Lanes.tsx:330-333`) | desktop | The engine ignores `$name`, so "Steer codex-2" tells the lead and nothing reaches codex-2 | "Hand to…" (D6) and steer as an atomic protocol op on the lead's queue (D4) | not ported | P3 |
| **Workflow cards** that append prompt boilerplate to your first message (`desktop-shell/src/renderer/workflows.ts:43-150`) | desktop | The protocol is enforced by prompt text, and the boilerplate appears as your own turn | Delegation by profile with a visible task card (D6) | not ported | P3 |
| **Side chat as a hidden thread** (`ui/SideChat.tsx`) | desktop | A thread you cannot see in the sidebar | An explicit sub-thread | not ported | P3 |
| **Five model-picker entry points** on the desktop (composer, lane chip, race builder, profiles editor with its own fetch, Settings text) and five routes on the TUI (`ctrl+l`, `ctrl+m`, palette, the "Reasoning" command, the dead `$` path) | desktop, TUI | Two implementations, different "current" rows, and `ctrl+m` is dead without keyboard enhancements (`keys.go:151`) | One popover (desktop), one key (TUI), one sheet (phone), all driven by `SetLead`/`SetMemberSpec` (D5) | TUI ~100 | P2, P3 |
| **The orchestration level set in three places** (composer, Tasks tab, Settings default); on the TUI only via `o` in a focused Tasks pane (`pane_tasks.go:81-93`) | desktop, TUI | Same control, three homes | One place: the thread menu (the Settings default stays) | small | P2, P3 |
| **Art directions and mix-and-match appearance**: Liquid Glass, Riso, Stitch; the LookEditor's seven mixable aspects; 10+ palettes and 9 diff themes; `WallpaperPicker`, `AppIconPicker`, `YoloFx`; 18 MB of art in `desktop-shell/src/renderer/art`; the TUI's theme editor and the yolo shockwave (`internal/ui/model/header.go:222`, `ui.go:860`) | desktop, TUI, phone | A combinatorial test surface and a look that changed three times in three days; glass blur re-composites on every streamed repaint | Chapter 04's tokens: grey chrome, one green-teal accent, light and dark, no glass, no continuously repainting animation (D18) | TUI ~500; desktop dies at M5 | P2, P3 |
| **Two layout systems**: Settings › Appearance › Layout calls `resetGlobalLayout()` and wipes the pane tree (`desktop-shell/src/renderer/layout/store.ts:124-129`) | desktop | They fight | One per-room layout (GPUI docks) | not ported | P3 |
| **Analytics heat maps** (`ui/Analytics.tsx`) | desktop | A second usage product | The Usage and Limits views, which stay (§6) | not ported | P3 |

### 1.6 The clients

| Item | Area | Why it goes | Replaced by | ~Lines | Phase |
|---|---|---|---|---|---|
| **desktop-shell** (Electron) | desktop | Built in about 12 days, its look changed three times, and its bugs are mostly architectural (chapter 01). It stays the feature reference until the GPUI app reaches parity on chapter 06's carry-over list | The GPUI app (D2) | ~66,500 (46,545 non-test TS in `src`, 10,925 test lines in `src`, 9,111 in `test/`) plus 18 MB of art | P3 M5 |
| **The Gio desktop app** | desktop | Already deleted in `eec5677`. No references remain in the README or docs | — | 0 | done |
| **The v2 per-PC PWA**: `pkg/remote/web/src/ui`, `controller.ts`, `state.ts`, `rpc.ts`, the v2 half of `protocol.ts` | phone | Fixes land here and not in the native app (`2a176f8`, `19332d8`, `a8d20ff`); it speaks the deleted v2 wire | `apps/mobile` (D11, D19) | ~4,500 | P6 |
| **The v3 hosted PWA**: `pkg/remote/web/src/hosted` | phone | A third client with none of the question, sub-thread or hand-off code | `apps/mobile`. If a web client is wanted, it is the Expo web build of the same app, never a separate code base | ~2,200 | P6 |
| **Shared `@web` modules used only by desktop-shell** (`theme`, `state`, `controller`, `transcript`, `mentions`, `handoff`, …) | shared TS | Their only consumer is deleted; their logic moves into the engine as structured events | Engine events; Rust and TS renderers read typed parts | (in the rows above) | P3 M5 |

---

## 2. Removal order for team connect

This is research-team's order with one change: **the messaging and contract
cuts (steps 1–3) land early, in P1**, so the foundation work does not port
dead ops and RPCs into the new protocol; **the access and transport cuts
(step 4) land in P4, in the same release as Deck Link roles.**

### Step 0: move pkg/remote's helpers out of peer (no behaviour change)

`pkg/remote` imports peer helpers. Delete peer first and the phone build
breaks. Move, with their tests:

| Helper | From | To |
|---|---|---|
| `Lockout`, `LockKey` | `internal/deck/peer/shared.go`, `lockout.go` | `pkg/remote/auth/lockout.go` |
| `RestrictPrivate`, `WritePrivateFile`, private file helpers, `fsperm_unix.go`, `fsperm_windows.go` | `peer/shared.go`, `files.go`, `fsperm_*` | `internal/privfile` |
| `NewPairCode` | `peer/pair.go:118-160` | `pkg/remote/auth` |
| `InviteHosts` | `peer/invite.go:200-275` | `pkg/remote/server` (then the Deck Link invite code) |

Repoint every importer outside the peer package (verified with `grep`):
`pkg/remote/auth/{code,limit,store,tls}.go` and their tests,
`pkg/remote/server/{server,run}.go`, `pkg/remote/identity/identity.go`,
`pkg/remote/projects/projects.go`, `pkg/remote/devstore/devstore.go`,
`pkg/remote/queue/queue.go`, `pkg/remote/ipc/ipc.go`,
`internal/mobilecontrol/managed.go`, `internal/workspace/remote_caps.go`.
About 800 lines move; nothing is lost. Fix the helper references in
`docs/remote-contract.md` R5 and §5 in the same PR.

**Done when:** `go build ./pkg/remote/...` passes with no import of
`internal/deck/peer` anywhere under `pkg/`.

### Step 1: unwire messaging and contracts from the engine and agents (P1)

- **Engine:** delete `internal/deck/engine/team.go`, `contract.go` (+ test),
  `ops.go` (+ test). In `engine/engine.go`, remove the chat, contract and ops
  wiring inside the `startPeers` block (`:125-200`) and the
  `mgr.SetTaskEnd(deals.taskEnd)` hook (`:133`). Delete `Engine.Waiting`
  (`:187-192`) and the outbox wait in `deck serve`'s drain. Keep `Drain` and
  `Busy`.
- **Session:** delete `session/contract.go` (+ test; its `SetTaskEnd` hook has
  no other caller), `session/opscontract.go` (+ test), `session/team.go`
  (+ test). Remove the teammate and team lines from
  `session/briefing.go:356-450` (the `mates` block at `:377-382` and the
  paragraph at `:445`).
- **Hub:** delete `hub/team.go` (+ test). In `hub/hub.go` remove
  `OpTaskContract` (`:56-57`), `OpShRun` and `OpContractResult` (`:65-67`),
  the `OpsRunner` type and `Env.Ops` (`:114-150`), the `opsOps` table
  (`:235-241`), the `task.contract` entry (`:352`), and the `team.read`,
  `team.post`, `peers.list`, `peers.status` entries (`:431-440`).
- **Peer package:** delete `team.go`, `outbox.go`, `contract.go`,
  `opscontract.go`, `opsparse.go`, `audit.go` and their tests; remove their
  handlers from `handler.go` and the default allow list (`handler.go:34-35`).
- **vsh:** delete `internal/deck/vsh` entirely, including `testdata/fakeshim`.
- **Agents:** delete the `ContractSafe` and `OpsSafe` flags from the claude,
  codex, opencode, acp and fake adapters.
- **Store:** remove `Thread.Contract` and `Thread.Ops`
  (`store/store.go:198-203`) and `Task.Remote` (`store/task.go:146-149`),
  after the load-time migration in §3 has run on them.
- **Config:** remove `peers.sandbox` (`PeerSandbox`, `OpsPresets`,
  `config.go:323-365`) and `peers.remote[].max_scope`.
- **Keep:** `session/share.go` and the shared-thread path stay wired until
  step 4. `peer/sub.go` (capsule transfer) stays; it moves in step 4.

### Step 2: delete the messaging and contract UI (P1)

- **TUI:** `internal/ui/model/pane_chat.go` (+ test); the contract cards and
  the `c`, `o`, `y`/`n`/`a` keys of `pane_team.go` (`:298-480`); the contract
  inbox items. Take Team chat out of the default columns (`pane.go:80`).
- **Desktop:** the chat and contracts parts of `ui/Team.tsx`; the contract
  inbox kind in `ui/Inbox.tsx`; `answerContract` in `ui/App.tsx:298-299`;
  the chat and contracts halves of `crew.ts` and `crewState.ts`; the palette
  entries in `palette.ts:50-81`; the `TeamPost`, `ProposeContract`,
  `AnswerContract` entries in `shared/bridge.ts`.

### Step 3: trim the proto (P1)

Remove from `proto/deck/v1/deck.proto`:

- **RPCs (3):** `TeamPost`, `ProposeContract`, `AnswerContract` (`:91, :96-100`).
- **Messages (7):** `TeamChat`, `TeamContract`, `TeamPostRequest`,
  `TeamPostResponse`, `ProposeContractRequest`, `ProposeContractResponse`,
  `AnswerContractRequest`.
- **Fields of `TeamResponse`:** `chat = 6`, `contracts = 7`,
  `contract_kinds = 8`, replaced with
  `reserved 6, 7, 8; reserved "chat", "contracts", "contract_kinds";`.

Then the matching half of `pkg/rpc/team.go:279-455` and `team_test.go`, and
`buf generate` (`buf.gen.yaml`) to refresh `pkg/rpc/deckv1`.

### Step 4: delete the peer transport, shared threads and commands (P4, with Deck Link roles)

Only after the Deck Link roles, invite and direct access are on main and
tested across two machines (§4).

- **Peer package:** delete what is left of `internal/deck/peer` (server,
  conn, stream, lockout leftovers, mcp, handler, identity, store, pair,
  invite, relay, supervisor, manager, control, service, ops, presence, cli,
  share, doc and every test, including `hostile_test.go`,
  `hardening_test.go`, `fuzz_test.go`, `transport_test.go`). **Before that,**
  move the capsule transfer (`sub.closure`/`sub.objects`, `peer/sub.go` +
  test) onto Deck Link for moving threads (D17).
- **Engine:** delete `engine/share.go` (+ test), the rest of `startPeers`,
  the `peers *peer.Service` field and `Engine.Peers()` (`engine.go:42, 50`).
- **Session:** delete `session/share.go` (+ test). Remove `Thread.Shares`
  and `store.Share` (`store.go:205, 227`).
- **Commands:** delete `internal/cmd/peer.go` (+ test). Replace the peers gate
  in `serve.go:201-208` and `reloadPeers` (`:186-198`) with `deck host run`;
  keep `deck serve` as a deprecated alias for one release.
- **Config:** delete `Config.Peers` (`config.go:142-145`), the types at
  `:296-500`, and `config/peers_test.go`. Keys gone: `peers.enabled`,
  `peers.name`, `peers.listen`, `peers.relay`, `peers.share_limits`,
  `peers.remote[]` (`name`, `addr`, `dial`, `role`, `via`, `relay`,
  `allow_inbound`, `max_scope`) and `peers.sandbox` (`ttl`, `scratch_mb`,
  `output_kb`, `presets`). Replace `Config.Teammates()` (`config.go:399`)
  at its callers (`internal/workspace/profiles.go:32,38`,
  `internal/deck/doctor/doctor.go:105`) with `nil`: profile names cannot
  collide with members any more, because members are people with an identity
  key, shown in the separate human palette (D18).
- **Doctor:** delete `doctor/peers.go` (+ test); `deck doctor` checks the
  Deck Link paths instead.
- **TUI:** delete `pane_team.go` (+ test), `pane_shared.go`,
  `dialog/settings_team.go` (+ test), `dialog/peer_pair.go`,
  `internal/workspace/peers.go`; the hooks in `ui.go` (`teamSnap` at `:386-388`,
  `teamSnapMsg` at `:911`, the pair dialog at `:1217` and `:5215-5216`); the
  teammate completions in `mentions.go:159-199`; the public/private teammate
  sharing in `pane_capsules.go` (the capsule list itself moves into the
  thread's Work view). Default columns lose Team and Shared (`pane.go:79-80`).
- **Desktop:** delete `ui/Team.tsx`, `ui/SharedRoom.tsx`, the rest of the team
  half of `crew.ts` and `crewState.ts`, the mate lanes (`lanes.ts`),
  `SharedGroup` (`ui/ProjectGroup.tsx`), `openShared` (`ui/useShell.ts`), the
  share controls in `ui/RoomHeader.tsx`, the Team button in
  `ui/SidebarFooter.tsx`, and the remaining entries in
  `shared/bridge.ts:152-161`. Rename the `teamGate.ts` "Team" switch to "Named
  agents": it also gates profiles. (If desktop-shell has already reached M5,
  this sub-step is moot.)
- **Proto:** remove the remaining **6 RPCs** (`Team`, `ShareThread`,
  `ThreadShares`, `ReadShared`, `PostShared`, `AnswerShared`;
  `deck.proto:87-104`) and **11 messages** (`TeamAgent`, `TeamThread`,
  `Teammate`, `TeamResponse`, `ShareThreadRequest`, `ThreadSharesResponse`,
  `ReadSharedRequest`, `SharedApproval`, `SharedMirror`, `PostSharedRequest`,
  `AnswerSharedRequest`; `:1154-1287`), the rest of the team half of
  `pkg/rpc/team.go` (the Board and Agents half above `:279` stays), and run
  `buf generate`. With step 3 that is **9 RPCs and 18 messages**.

### Step 5: retire `deck remote` v2 (P6)

When the PWAs are gone and the desktop pairs through Deck Link: delete
`internal/cmd/remote.go` (+ test), the v2 parts of `pkg/remote/server`, the
v2 parts of `pkg/remote/wire`, and desktop-shell's `main/pair.ts` and
`main/pin.ts` if desktop-shell still exists. `<data dir>/remote/` is retired
like `~/.deck/peers` (§3).

### Step 6: docs (with each step that makes them false)

| Doc | Action | Step |
|---|---|---|
| `docs/peers-design.md` (145 lines) | Delete | 4 |
| `docs/multiplayer-design.md` | Cut the peers row of §1, the peer parts of §2, all of §8 (`:240-354`), the peer rules in §9, phases 6–9 of §10, the peer risks in §11. Keep §3–§7 (pools, subagent trees, leases and presence, races, budgets and brakes) | 1 (contracts, chat), 4 (rest) |
| `docs/sub.md:9, 105-115` ("Between Decks") | Rewrite as "Between hosts": capsule transfer over Deck Link for moving a thread | 4 |
| `docs/remote-contract.md` | Step 0: fix R5 and §5 helper references. Step 5: delete; `docs/link-contract.md` replaces it | 0, 5 |
| `docs/mobile-lan.md` | Rewrite for `deck relay` (no Node, no wrangler) | P4 |
| `docs/mobile-contract.md` | Keep; update pairing to the invite (role, project) | P4 |
| `docs/design/agents-and-mentions.md` §2 "Addressing" | Delete §2; keep profiles (§1) and `#` context (§3) | P2 |
| `docs/examples/deck-serve.service`, `deck-serve.container` | Rewrite: no "turn peers on", no `deck peer pair`; this becomes the template `deck host up` generates (D16) | 4 |
| `README.md` | Delete "Linking two Decks through a relay" (`:297-308`) and "Working with teammates" (`:316-320`); rewrite "A Deck on a server" (`:295`) and "`deck serve`" (`:310-314`) for `deck host`; remove the `task.contract`, `peers.*` and `team.*` rows from "How agents reach Deck" (`:255, 267, 280-283`); remove the teammate-name rule from named agents (`:214`); remove the `peers` config paragraph (`:382`); update "Phone remote" (`:287`) and the desktop paragraph (`:400`) | 1, 4, 5 |
| `desktop-shell/README.md:15-26, 92, 741` and `desktop-shell/docs/{plan,design-apple,design-glass}.md` | Deleted with desktop-shell. Before M5, carry the rules worth keeping (one accent, agent hue as glyph or rail only, mono for machine text, the Work Graph spec, the "not AI-template" list) into chapter 04 | P3 M5 |
| New: `docs/fleet-design.md`, `docs/link-contract.md` | Write (chapter 05) | P4 |

### Tests that go, and what replaces them

- **Deleted:** all tests in `internal/deck/peer` (~6,200 lines), `vsh_test.go`
  (656) and `testdata/fakeshim`; `session/{contract,opscontract,team,share}_test.go`;
  `engine/{contract,ops,share}_test.go`; `hub/team_test.go`;
  `config/peers_test.go`; `doctor/peers_test.go`; `internal/cmd/peer_test.go`;
  the team half of `pkg/rpc/team_test.go`; `internal/ui/model/{pane_team,pane_chat}_test.go`;
  `internal/ui/dialog/settings_team_test.go`.
- **Changed:** `session/briefing_budget_test.go` and the start-instruction cap
  tests (the team lines leave every briefing). The desktop e2e specs that
  touch the team page, shared threads or contracts, and the 9 specs that
  click "Pair with a Deck" (`desktop-shell/test/e2e/`), in the same PR as the
  UI they test.
- **Carried forward as patterns, not code:** the hostile and fuzz cases of
  the peer server (pre-auth limits, lockout, oversize frames, a revoked key
  dropped within seconds) become Deck Link tests next to the v3 daemon's
  existing `gate_test.go` and `pair_test.go`.

### Order of the other cuts

| Phase | Cuts |
|---|---|
| P1 | Team messaging and contracts (steps 0–3). The three stores, the converters on the desktop path, the model caches, engine attention, whole-config saves, engine mention routing, the contract rename, legacy proto fields, the prose hand-off writer. Polling goes as each event lands |
| P2 | The Crush event path and services under the TUI, Crush model/Thinking leftovers, LSP renderers, `@`/`$` leftovers in the TUI, TUI picker routes, the TUI theme editor and shockwave, the hand-off regex in `chat/cards.go`, `agents-and-mentions.md` §2 |
| P3 | Nothing is deleted from desktop-shell before M5 except steps 2 and 4 (it is frozen). The GPUI app does not port the surfaces in §1.5. At M5: delete `desktop-shell/` and the `@web` modules only it uses |
| P4 | Step 4 and its docs, the `deck serve` peers mode, the wrangler relay, `deck mobile` renamed |
| P6 | The two PWAs, `deck remote` v2 (step 5), `pkg/remote/wire` and the adapter, the phone's `$name` rail, `roster.add` |

---

## 3. Migration for existing users

All of this runs once, on the first start of the version that ships the
matching step. A marker file, `~/.deck/migrations/<step>`, makes it
idempotent. The engine emits the notice as an event, so every client shows
it (the TUI as a notice, the desktop as a banner, the phone in Inbox), and it
is also logged once.

### 3.1 With step 1 (P1)

| Data | Action |
|---|---|
| Threads with `Thread.Contract != ""` (a teammate's code contract runs or ran here) | Set `Archived` and a new `Frozen` reason (`"teammate contract"`). A frozen thread opens for reading and refuses posts, Continue and agent resume. The same `Frozen` field later marks threads moved to another host (D17: "moved to vps-1") |
| Threads with `Thread.Ops == true` | Same, reason `"ops contract"`. This must run before the lockdown code is deleted, or those agents would resume with full tools |
| Tasks owned by a teammate (`Task.Remote != ""`, owners like `vps` or `vps/claude`) | Mark failed with the reason "teammate contracts were removed in Deck v3". Dependants then follow their normal `OnFail` path instead of waiting for a run the local scheduler never starts |
| `~/.deck/peers/chat.jsonl`, `outbox.json`, `contracts.json`, `audit.jsonl` | Left in place, unread. Step 4 moves them with the rest |

### 3.2 With step 4 (P4)

| Data | Action |
|---|---|
| `~/.deck/peers/` (`id_ed25519`, `pinned.json`, the files above) | Rename to `~/.deck/peers.retired-<date>/`. Delete `control.sock`. Pins are **not** migrated: they are Deck machine keys, not people, and Deck Link grants are keyed by a person's identity key (D15). `deck doctor` mentions the retired folder until you delete it |
| `peers` key in `config.json` | The loader is not strict (no `DisallowUnknownFields` in `internal/deck/config`), so the key is ignored on read and dropped on the next save. If it had `enabled: true`, the notice says so |
| `Thread.Shares` | Dropped: the names cannot be bound to people. The notice lists the threads that had shares so the owner can invite those people as members |
| Your own phones (v3 `devstore`) | Kept. They become devices of the owner member; nothing to re-pair |

### 3.3 With step 5 (P6)

| Data | Action |
|---|---|
| `<data dir>/remote/` (v2 devices, VAPID keys, push subscriptions) | Renamed to `remote.retired-<date>/`. v2 devices must pair again with `deck invite` |
| Desktop `servers.json` (v2 pairings) | The GPUI app reads it once and offers "Re-pair this Deck with an invite" per entry |

### 3.4 The one-time notice

Step 1 (shown only if any thread, task or `~/.deck/peers` file was affected):

> **Team chat and contracts were removed in Deck v3.** Deck no longer sends
> or receives team chat, code contracts or ops reports. {N} thread(s) that
> ran a teammate's contract are archived and read-only. {M} task(s) a
> teammate owned were marked failed. Shared threads still work until the
> next update replaces them with direct access.

Step 4:

> **The peer link was replaced by Deck Link.** Port 7722 is closed and
> `deck peer` is gone. Your old peer files are in
> `~/.deck/peers.retired-{date}/`; Deck no longer reads them. To give someone
> access to this host's threads, run `deck invite --role member` (or open
> Host › Members › Invite). People you invite run this host's agents on your
> plans. These threads were shared under the old link and are no longer
> shared: {list}.

---

## 4. Risks, and how we avoid them

| Risk | How we avoid it |
|---|---|
| **A gap with no teammate access**: shared threads deleted before Deck Link has roles | Step 4 is gated. Its PR merges only after the Deck Link invite, roles (viewer / member / admin), direct access to the full thread and presence are on main, tested between two machines, and in the same release tag. Until then the old shared-thread path keeps working, with messaging already gone. If Deck Link slips, step 4 slips with it; steps 0–3 do not depend on it |
| **The phone build breaks** because `pkg/remote` imports peer | Step 0 lands first, alone, with the `go build ./pkg/remote/...` check |
| **Contract and ops threads resume with full tools** once the lockdown code is gone | The §3.1 migration runs in the same release as step 1, before any thread resumes |
| **Dependants wait forever** on a teammate-owned task | Those tasks are failed on load (§3.1) |
| **Capsule hand-overs break** when `address.go` goes: `capsulework.go:566` posts `"@name …"` | Convert the capsule hand-over to a structured hand-off (a task with terms) before deleting `address.go` |
| **Usage history is lost** with the Crush store: `internal/usage` reads the `usage` tables in `internal/db` | Move the usage tables to the host daemon's own database first; only the `sessions`, `messages`, `files` and `read_files` tables are dropped |
| **The native app breaks** when the PWAs go: it imports `hosted/pairing` and the v2 `protocol.ts` types (`apps/mobile/src`) | Move the modules `apps/mobile` uses into one shared core package first, then delete what nothing imports |
| **Old threads stop loading** after the contract rename | The loader keeps reading the `contract` JSON key into `Terms` |
| **People relied on ops contracts** for server reports | Say so in the notice and the release notes. The replacement is an admin opening a thread on that host now, and Routines later |
| **Teammates spend the owner's plans** under direct access | The invite says so (D15), and plan windows are visible to members |
| **Losing the peer server's hardening** | Its hostile and fuzz cases are rewritten for Deck Link (§2, tests) before step 4 merges |
| **Briefing size changes** silently | `briefing_budget_test.go` is updated in step 1 with the new expected sizes |

---

## 5. Why access cuts land with Deck Link, and messaging cuts do not

The owner wants messaging gone, and nothing depends on it: no workflow other
than team chat and contracts uses the outbox, the contract stores or vsh. Cutting them first
shrinks the engine, the briefing and the proto before P1 reworks the protocol,
so nobody ports `TeamPost` or `ProposeContract` to the new streams.

Shared threads are different: they are the only teammate access Deck has
today, weak as it is. Removing them before Deck Link roles exist would leave
teams with nothing, so they leave in the release that brings the
replacement.

---

## 6. What v3 keeps (do not delete)

These sit close to deleted code. They stay, and any file that holds them is
split, not deleted.

| Keep | Where it lives | Note |
|---|---|---|
| **Subagents** | `internal/deck/session/spawn.go` | Delegated subagents inherit the parent's spec (D5) |
| **Sub-threads** | `session/subthread.go` | Get structured report events instead of prose |
| **Races** | `session/race.go`, `StartRace` with entrants | Only the legacy `kind`/`count` fields go |
| **Task board** | `store/task.go`, the scheduler | Only `Task.Remote` goes; tasks become the unit of delegation (D6) |
| **Hand-off terms and evidence** | `session/handcontract.go`, `handoff.go`, `evidence.go`, `store.Contract` | Renamed to `Terms` (§1.4), not removed |
| **Leases and the edit gate** | `session/leases.go`, `session/coord.go` | |
| **Budgets and brakes** | `session/budget.go`, `session/brakes.go` | Brakes also rate-limit members' messages under direct access |
| **Worktree isolation** | `session/work.go`, `IsolateSession`/`ApplyIsolated` | |
| **Capsules** | `internal/deck/sub`, `session/capsulework.go`, `session/subops.go`; the transfer in `peer/sub.go` moves to Deck Link | Used to move threads between hosts (D17) |
| **Noise pairing and the encrypted channel** | `pkg/remote/daemon/pair.go`, `pkg/remote/e2e`, `pkg/remote/identity`, `pkg/remote/devstore`, `pkg/remote/daemon/gate.go` | The base of Deck Link (D13) |
| **Passkeys** | `pkg/remote/passkey`, `pkg/remote/policy` | Admin approvals of destructive actions (D15) |
| **Offline queue and snapshots** | `pkg/remote/queue`, `pkg/remote/web/src/offline`, `pkg/remote/daemon/snapshot.go` | The envelopes stay; their payloads become proto messages |
| **Push** | `pkg/remote/push` | Extended to native push (D11) |
| **The relay** | `pkg/remote/relay` (hosted, optional), `pkg/remote/relayclient` | `deck relay` speaks the same `relay/v1` |
| **Usage views** | `internal/usage`, the `usage` tables, TUI `pane_usage.go` and the Usage dialog, the desktop Usage page (carried to GPUI) | Only the desktop's Analytics heat maps are not ported |
| **The helpers moved in step 0** | `internal/privfile`, `pkg/remote/auth` | |
| **`deck serve`'s drain logic and the shared store lock** | `internal/cmd/serve.go:236-262`, `store.Lock` | Carried into `deck host run` |
| **The engine-side approval broker** | `internal/deck/approve` | Approvals become per-thread events; the broker stays |
