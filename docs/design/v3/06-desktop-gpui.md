# 06. The desktop app in Rust and GPUI

Status: plan. Owner decisions D2, D3, D4, D5, D7, D10, D13, D14 and D18 apply (see `decisions.md`). This chapter covers how the app is built: crates, threads, data flow, the screens and components, performance budgets, testing, milestones and the release changes. The look (tokens, type, spacing, motion, wireframe styling) is in [04-design.md](04-design.md). How a client reaches a host (Deck Link, path order, pairing, roles) is in [05-fleet-and-connect.md](05-fleet-and-connect.md). Here we only say what the desktop needs from those.

Evidence labels used below:

- **[V]**: verified against a primary source (the source file, the crates.io API, or the official repo) on 2026-11-05.
- **[S]**: from search-result summaries or third-party posts. Not verified.
- **[U]**: unverified, or an estimate of ours. Check it before relying on it.

---

## 1. Summary

- We write a new Rust app on GPUI and talk to `deck` daemons over the v3 protocol with tonic. desktop-shell (Electron, 46.5k lines of TS) is the feature reference. We delete it at M5, once the carry-over list (§6.1) reaches parity.
- The code lives in a Cargo workspace at `desktop/`. The name is free since the Gio app went in `eec5677`. It has five crates: `deck-desktop` (the binary), `deck-ui` (components), `deck-state` (an event-sourced store with no GPUI dependency), `deck-client` (tonic, Deck Link transports, the multi-host connection manager) and `deck-proto` (generated code).
- The engine owns all derived state: attention, running, unread and ordering. The app applies revision-stamped snapshots and deltas and computes none of these itself. This removes the bug classes in §1 of research-desktop.md by construction, not by care.
- Only visible rows are laid out. Streaming text is appended to a per-part buffer, and only the touched rows are re-measured. Nothing holds a whole transcript.
- Budgets: about 0.4 s to a usable window, at most 120 MB idle (macOS), and a p99 main-thread frame of at most 8 ms while 5 threads stream. CI enforces them on every PR (§5).
- The agent browser and Preview are frames streamed from headless Chromium on the host (D10). There is no webview in the app.
- Six milestones run M0 to M5. Both desktop apps ship until M5. `release.yml` gets a `desktop-gpui` job beside the Electron job, and the Electron job is removed at M5.

---

## 2. Why GPUI, the risks, and how we handle each

### 2.1 Reasons

1. **The #1 complaint is bugginess.** Most desktop-shell bugs are shell architecture: ~16 hand-synchronised module stores, monkey-patched `dispatch`, remounts on project switch, popovers clipped by `overflow-hidden`, nine global `keydown` listeners and no Escape layer stack (research-desktop.md §1, B8 `appState.ts:247-254`, B11 `ui/App.tsx:407-408`, B15 `ui/kit/menu.tsx:62-101`, B16, B17). GPUI gives us one owned state model (entities), a focus tree with key contexts, and deferred and anchored overlays. Rust's type system makes "two stores disagree" hard to write by accident.
2. **Speed and RAM by default.** GPUI draws on the GPU with platform text shaping, and only what is on screen is laid out (`uniform_list`, `list`). Electron's floor (main, GPU, utility and renderer processes, ~350–600 MB estimated in research-desktop.md §5) disappears. Zed's third-party numbers (0.12–0.4 s cold start, ~142–222 MB idle) are **[S]** with no published method. We measure ourselves (§5).
3. **Zed has already built most of this app.** It has an agent panel with streaming, a per-hunk review multibuffer, a terminal on `alacritty_terminal`, docks and panes, a keymap with context predicates, a command palette and collab "follow". We read those as references. We copy no code from GPL crates (§2.2, risk 3).
4. **There is a component library.** gpui-component (Longbridge's GPUI Kit, Apache-2.0) has inputs, a code editor, virtual lists, tables, docks, Markdown, menus, popovers and dialogs. It runs in a shipping commercial app (Longbridge Pro) **[V: README]**.
5. **The same proto everywhere (D3).** A native Rust client on the generated proto keeps us honest. Nothing in the desktop can lean on in-process engine calls or a shared TypeScript reducer.

### 2.2 Risks and mitigations

| # | Risk | What we know | Mitigation |
|---|---|---|---|
| 1 | **Pre-1.0 API churn** | The README says it is "still pre-1.0" and that "there will often be breaking changes between versions" **[V]** (`crates/gpui/README.md`). | Pin exact versions. Only `deck-ui` and `deck-desktop` import `gpui`; `deck-state` and `deck-client` never do. Bump once a month in one PR, gated by the screenshot tests (§7.4). Budget 1–2 engineer-days per bump. |
| 2 | **The crates.io `gpui` is stale** | `gpui` on crates.io is **0.2.2, published 2025-10-22**, and nothing newer has been published **[V: crates.io API]**. Upstream `main` has moved far since: platform crates were split out (`gpui_macos`, `gpui_linux`, `gpui_windows`, `gpui_wgpu`, `gpui_platform`) and Linux moved to wgpu. gpui-component depends on **`gpui-pre`**, a weekly crates.io snapshot of Zed's gpui published by a Longbridge maintainer (0.3.11, "snapshot of zed@f816e1f", 2026-11-02) **[V]**. | Use **`gpui = { package = "gpui-pre", version = "=<exact>" }`**, the same version gpui-component 0.9.x pins (`=0.3.11` today **[V]**). Run `cargo vendor` into the CI cache so a yanked crate cannot break a release. Fallback if `gpui-pre` stops: a git dependency on `zed-industries/zed` at the same commit. Patches in Zed's root `[patch.crates-io]` (forks of `async-task` and `async-process`, among others, at `Cargo.toml:1037-1045` **[V]**) do **not** carry over to dependents, so we copy the ones gpui needs into our workspace. |
| 3 | **Licence** | `gpui`, `gpui_macros`, `sum_tree`, `util`, `collections`, `refineable`, `http_client` and `gpui_tokio` are **Apache-2.0**. Zed's `ui`, `editor`, `terminal`, `terminal_view`, `markdown`, `theme`, `rope`, `picker`, `auto_update` and `release_channel` are **GPL-3.0-or-later** **[V: each crate's Cargo.toml]**. Deck is proprietary (`LICENSE`). | We **do not depend on, or copy from, any GPL Zed crate**. They are design references only, read for ideas and never pasted. `cargo deny` with a licence allow-list (Apache-2.0, MIT, BSD, ISC, Zlib, Unicode, MPL-2.0 file-level) fails CI on any GPL or AGPL crate. `cargo about` generates the third-party notices into the bundle. research-look.md §6 suggests borrowing Zed's `ui` crate (`ListItem`, `PopoverMenu`, …); that means borrowing the *pattern* in our own `deck-ui`, not the crate. |
| 4 | **Windows and Linux maturity** | Windows: Zed is stable on Windows with a DirectX 11 renderer and DirectWrite **[S: zed.dev blog/Neowin]**. The README says Windows "uses Win32 and text uses DirectWrite" **[V]**. Linux: Blade was removed and the renderer was reimplemented on **wgpu** (PR #46758, merged 2026-02-13) **[V: commit]**. In review, an empty wgpu window measured ~100 MB against ~10 MB on GPUI's own renderer **[S]**. macOS: Metal **[V]**. | All three OSes build and test on every PR. Nightly performance runs on real GPUs (§5.2). Linux runs smoke tests under both X11 and Wayland. Supported: macOS 13+, Windows 10 22H2+ with a DX11 GPU, and Ubuntu 22.04+ / Fedora 40+ (glibc 2.35 baseline). Linux's RAM budget is set 40 MB higher than macOS (§5.1) until we have measured it. |
| 5 | **No webview** | GPUI has none. gpui-component's `webview` feature wraps wry, "only supports macOS and Windows currently", and draws *over* the GPUI window **[S: gpui-wry README]**. | We don't use it. The agent browser and Preview are host-side headless Chromium streamed as frames (D10, §3.9). Links open in the system browser. |
| 6 | **Accessibility** | Upstream `main` now wires AccessKit adapters on all three platforms (`accesskit_macos`, `accesskit_windows`, `accesskit_unix` in the platform crates; `A11yCallbacks` and `a11y_tree_update` in `platform.rs`) **[V]**. Elements opt in with a stable id and a role **[S: fork docs]**. How much of Zed's own UI is covered is unknown **[U]**. gpui-component says "AccessKit roles, names, states, relationships, and actions are built into the interaction layer and covered by tests" **[V: README]**. | Every `deck-ui` component sets a role and an accessible name. That is part of its definition of done. The status indicator always has a text label (04 forbids colour-only status). CI runs an a11y-tree snapshot test (§7.3), and we do a manual VoiceOver and Narrator pass at each milestone. If upstream's API differs from the fork docs, we adapt in M0. |
| 7 | **IME and text input** | GPUI's `InputHandler` mirrors `NSTextInputClient`, and `EntityInputHandler` has the marked-text methods (`replace_and_mark_text_in_range`, `unmark_text`) **[S: docs snippets]**. Linux uses `xim` under X11 (`gpui_linux` Cargo features **[V]**) and Wayland text-input. Wayland candidate-window placement is a known weak spot across the whole ecosystem **[S: fcitx issues]**. | The composer is built on gpui-component's `Input` (multi-line, IME-aware), wrapped in `deck-ui::Composer`. An IME test matrix (Japanese, Chinese pinyin, Korean, dead keys) runs by hand on every OS at M1 and M5. Wayland IME gaps get a known-issues entry; we don't block on them. |
| 8 | **Cross-block selection and rich text** | Zed's markdown crate implements its own selection (research-look.md §6). gpui-component ships `text_selection` and `stream-markdown` examples **[V: workspace members]**, but how complete they are is **[U]**. | We designed selection into the timeline from M1 (§3.6), with a spike in week 1 of M1 to decide whether gpui-component's selection is enough. |
| 9 | **Velocity becomes churn** (the desktop-shell lesson: 196 commits in 12 days, three visual directions in three days, five picker redesigns) | research-desktop.md §0 | The design is frozen in 04 before M1. A component gallery binary (`deck-ui-gallery`) is the review surface for visuals. After M2, visual changes need the owner's sign-off and an updated screenshot golden in the same PR. |
| 10 | **Debugging without devtools** | GPUI has an `inspector` feature **[V: Cargo.toml features]**. | Dev builds enable `inspector`. `tracing` spans cover apply, layout and paint, plus a frame-time overlay (`ctrl-alt-shift-F`). |
| 11 | **Platform glue GPUI lacks** | GPUI covers system notifications with action buttons, credentials (Keychain, Windows Credential Manager, Secret Service via `oo7`), menus and dock menus **[V: platform.rs and platform impls]**. `register_url_scheme` returns "unimplemented" on Windows and Linux **[V]**. There is no dock or taskbar badge API **[V: absent from platform.rs]**. | The installer registers `deck://` on Windows (HKCU) and Linux (`.desktop` `x-scheme-handler/deck`). The badge is written per OS in `deck-desktop/platform/`: `NSDockTile` via `objc2`, `ITaskbarList3::SetOverlayIcon` via `windows`, and the Unity LauncherEntry D-Bus API on Linux where it exists (a no-op elsewhere). |

---

## 3. Architecture

### 3.1 Crate layout

```
desktop/                       Cargo workspace (Rust edition 2024, toolchain pinned in rust-toolchain.toml)
├─ crates/
│  ├─ deck-proto/              tonic + prost code generated in build.rs from ../../proto (no checked-in output)
│  ├─ deck-client/             no gpui. tokio. Talks to hosts.
│  │   ├─ link/                Deck Link transports: unix socket / named pipe, TCP (tailnet, LAN, public),
│  │   │                       Noise relay, `ssh host deck link --stdio`; Noise IK handshake (snow)
│  │   ├─ conn.rs              HostConnection state machine, path racing, backoff, path badge
│  │   ├─ fleet.rs             ConnectionManager: N hosts, one HTTP/2 channel each
│  │   ├─ api.rs               typed wrappers over the generated client; client_op_id on every write
│  │   ├─ bootstrap.rs         "Add a host" over system ssh (D16), binary push, key exchange
│  │   ├─ daemon.rs            find or start the local daemon (bundled `deck`)
│  │   └─ record.rs            .deckrec recorder and replayer (tests, bug reports)
│  ├─ deck-state/             no gpui, no tokio. Pure, deterministic.
│  │   ├─ host.rs              HostStore: HostInfo, RunningSummary by thread, agents, inbox items (by host_rev)
│  │   ├─ fleet.rs             FleetView: merged (host_id, thread_id) keyed views for Inbox, Now, left rail
│  │   ├─ thread.rs            ThreadStore: snapshot@rev + events, message window, parts, tasks, approvals, queue
│  │   ├─ text.rs              append-only part buffers, line index, frozen/tail markdown blocks
│  │   ├─ pending.rs           optimistic ops keyed by client_op_id
│  │   └─ local.rs             drafts (text + attachment files), per-thread layout, window state, keymap overrides
│  ├─ deck-ui/                gpui + gpui-component. Components only; no RPC calls.
│  │   └─ (see §4.4 for the list)
│  └─ deck-desktop/           the binary: app init, windows, actions and keymap, settings, platform glue,
│                             updater, crash handler, notifications, deep links, bridges state↔ui
└─ tools/
   ├─ deck-ui-gallery/        every component in every state, light and dark (review + screenshot source)
   └─ deck-bench/             perf harness used by CI (§5.2)
```

Rules:

- `deck-state` takes events in and produces state out. It is tested with plain `cargo test` and property tests. It does not know GPUI exists.
- `deck-ui` components take view-model structs and callbacks. They never hold a client handle, which keeps them in the gallery and in screenshot tests without a host.
- `deck-desktop` is the only crate that wires the client, the state and the UI together.
- Generated code: `tonic-build` (or its 0.14-series successor `tonic-prost-build`, to be confirmed at M0 **[U]**) runs in `deck-proto/build.rs` with a vendored `protoc`. `buf.gen.yaml` does not change.

### 3.2 Threading model

```
 main thread (GPUI foreground executor)          GPUI background executor (thread pool)
 ─────────────────────────────────────           ─────────────────────────────────────
 entities: FleetModel, ThreadModel(s)            markdown parse of frozen blocks
 apply event batches (≤2 ms budget/frame)  ◀──┐  syntax highlight (visible lines only)
 layout / prepaint / paint                     │  word-level diff, image + frame decode
 input, focus, actions                         │  message search index
                                               │
 tokio runtime (2 workers, owned by deck-client)
 ─────────────────────────────────────────────
 per host: transport + Noise + HTTP/2 (tonic)
 WatchHost stream, WatchThread streams, unary calls
 ssh child processes, updater downloads
        │  decoded proto events, batched
        └──▶ async channel per host ──▶ foreground drain task
```

- **Two executors, deliberately.** tonic needs tokio, and GPUI has its own executors. We start one small tokio runtime with 2 worker threads, as Zed's Apache-2.0 `gpui_tokio` does **[V: source]**. We write the 40-line equivalent ourselves rather than depending on an unpublished crate.
- **The bridge.** Each host's tokio task decodes protobuf off the main thread and pushes `HostEvent` batches into an `async-channel`. A foreground task drains the channel and applies events in order. It stops after a 2 ms budget, so one frame's apply cannot blow the frame. The rest goes in the next frame. After applying, it calls `cx.notify()` once per touched entity, not once per event. A burst of 500 deltas is one notify.
- **Nothing blocks the main thread.** We enforce this with `clippy::disallowed_methods` in `deck-ui` and `deck-desktop` for `std::fs::*`, `std::thread::sleep`, `block_on` and `std::process::Command::output`.
- **Background work is cancellable.** Each job (highlighting a file, decoding a frame) holds a `Task`. Dropping the view drops the task. If a newer frame arrives, the decode of the old one is dropped (the latest frame wins).

### 3.3 Connections (what the desktop needs from Deck Link)

The full design is in [05-fleet-and-connect.md](05-fleet-and-connect.md). The desktop side works like this:

- **Path order (D14):** local socket, then tailnet (system `tailscaled` first, embedded tsnet as fallback), then direct LAN or public, then the Noise relay, then the SSH stdio tunnel (`ssh host deck link --stdio`). `HostConnection` races the next path 300 ms after the previous one starts (happy-eyeballs) and keeps the first that completes the handshake. The winning path is exposed as the host's path badge in the left rail.
- **Security:** remote paths run the Deck Link Noise handshake that is already in the phone stack: `Noise_IK_25519_AESGCM_SHA256` (`pkg/remote/e2e/e2e.go:17,233`). In Rust that is the `snow` crate. The desktop's device key and each host's pinned key decide trust. Network membership never does (D13). tonic runs HTTP/2 over the resulting encrypted stream through a custom connector (`tower::service_fn` returning our stream type), so every RPC and stream is multiplexed on one connection per host.
- **The local daemon (D3).** On launch, `daemon.rs` looks for the local socket (`~/.deck/deck.sock`, or a named pipe on Windows). If nothing is there, it starts the bundled `deck serve` as a per-user service: a launchd agent on macOS, a systemd user unit on Linux, a per-user logon task on Windows. Threads therefore keep running after the app quits. First run asks once: "Keep agents running when Deck is closed?" (default yes). If the user says no, the daemon runs as a child process and stops with the app.
- **"Add a host" (D16)** shells out to the **system `ssh`**, so `~/.ssh/config`, agents, `ProxyJump` and hardware keys all work. It runs the bootstrap described in 05. The app bundles only the `deck` binary for its own OS and architecture. To push a binary for a different target, the app downloads the exact-version release binary once, checks the checksum and signature, caches it and pushes it over SSH, so the VPS needs no egress.
- **Version skew.** `HostInfo.version` and `features[]` gate UI features. Clients support hosts on the current and the previous minor version. Older hosts show an "Update host" chip, and the app never updates a remote host without being asked.

### 3.4 The state model (event-sourced, by revision)

D4 makes the engine the single source of truth. The desktop's job is to apply what it is sent, in order, and to notice gaps.

```
WatchHost(host)                       WatchThread(thread, from_rev)
   │ HostInfo, RunningSummary upserts      │ GetThread → {snapshot, rev}
   │ AgentStatus, InboxItem (host_rev)     │ MessageAppended / PartDelta / MessageFinished
   ▼                                       │ TaskChanged, AgentChanged, Approval*, QueueChanged,
HostStore ──┐                              │ HandoffCard, TaskReport, Plan, TurnEnd, Presence
HostStore ──┼─▶ FleetView ─▶ Inbox, Now,    ▼
HostStore ──┘                left rail   ThreadStore (one per subscribed thread)
```

- **HostStore** holds summaries keyed by `thread_id`, and each upsert carries a `host_rev`. On reconnect it resumes from `host_rev`. Otherwise it takes the full summary set, which is small and has no bodies. It never recomputes attention or "running": it shows `RunningSummary.state` and `attention_reason` as sent. That fixes the three-ways derived state problem (research-engine.md §A6).
- **FleetView** merges the HostStores, keyed by `(host_id, thread_id)`. Every thread row carries its host ("on vps-1", D9). Inbox and Now are queries over FleetView, not separate lists. That fixes the triplicated attention (research-desktop.md §4c).
- **ThreadStore** works like this:
  1. `GetThread` returns a snapshot at rev R.
  2. `WatchThread(from_rev = R)` starts.
  3. Every event must have `rev == last_rev + 1`. Anything else is a gap: the store marks itself `resyncing`, keeps showing what it has, and re-subscribes from `last_rev`. The host replays from its durable log.
  
  There are no merge heuristics, no `updated_at` comparisons and no "drop the snapshot if a delta arrived". That removes B2 (vanishing approvals, `rpcClient.ts:156-166`) and B3 (ghost messages, `pkg/remote/web/src/state.ts:127-135`), because deletions arrive as events with a rev.
- **Subscriptions are bounded.** We hold `WatchThread` open for the visible thread, for every thread pinned with Follow, and for the last 3 viewed threads (kept warm for instant switching). Everything else is covered by the summary stream. This is how 50 running threads cost almost nothing (research-engine.md §A3: today the desktop gets every session's full stream).
- **Message window.** Each ThreadStore keeps the latest 300 messages plus any pages the user scrolled back to. Pages more than two screens from the viewport are evicted once the thread loses focus. Older history comes from `ListMessages(thread, before_rev, limit)`.
- **Large tool output.** A part over 64 KB is kept as its first 8 KB and last 8 KB plus a "Load all (1.2 MB)" row that fetches the full text from the host on demand. This follows the Codex app lesson (research-external.md §3.3: renderers at 4–12 GB from retained tool output).
- **Writes.** Every write carries a `client_op_id` (a UUID v7) and returns the resulting state plus a rev (D4). `pending.rs` shows the optimistic result marked "sending". When the echo event or the RPC result arrives, the pending op is replaced. On error it rolls back and shows the error inline on the thing that failed, in a toast only when nothing on screen owns it. Toasts are coalesced per host, so an unreachable host gives one banner, not a storm (B14).
- **Steer is one atomic op** (`Send{steer: true}` from the protocol chapter). It replaces the 3-second subscribe-and-steer race (B1, `ui/useRoom.ts:32,37-55`).
- **Local-only state** (`local.rs`): drafts per thread, including attachments, which are copied into the app's cache directory so they survive thread switches and restarts (fixes B10, `ui/Composer.tsx:150`). Also the per-thread pane layout (fixes B12 and B13), window state and keymap overrides. All JSON files live in the platform config directory and are written atomically (write to a temp file, then rename). The last summary set per host is cached so cold start can paint at once, marked stale until the stream confirms it.

### 3.5 Streaming text: from delta to pixels

```
PartDelta{msg, part, rev, append}           (tokio: decoded off main thread)
  → channel → foreground drain (budgeted)
  → ThreadStore.apply: part.text.push_str(append); line_index.extend(...)
                       markdown: blocks before the last open block are FROZEN,
                                 only the tail block is re-parsed
  → ThreadModel emits Changed{rows: [ix]}   (which timeline rows, not "everything")
  → Timeline: list_state.splice(ix..ix+1, 1) → that row is re-measured next frame
  → GPUI lays out only rows in the viewport + overdraw; the shaped-line cache
    means unchanged lines are not reshaped
```

- **Rows are blocks, not messages.** A reply is split into timeline rows at block boundaries: a paragraph group, a code block of at most 60 lines, a list or a table. A 5,000-line reply then becomes many small rows, and only the visible ones exist as elements. Streaming touches only the last row of the last message.
- **Markdown.** We parse with `pulldown-cmark` (MIT) in `deck-state` (pure), producing a block list. Frozen blocks are parsed once on the background executor and cached by `(msg_id, part, block_ix)`. Rendering uses gpui-component's Markdown elements where they fit; the `stream-markdown` example is the starting point. If they don't fit, we write our own `StyledText`-based renderer in `deck-ui`. The block-freezing logic is ours either way, so swapping the renderer is local.
- **Code highlighting** uses gpui-component's `syntect` feature (about 200 languages for a few MB, line-oriented **[V: crate feature comment]**). We don't need tree-sitter in a chat and diff app. Highlighting runs only for visible lines and is cached per block.
- **Scroll.** The timeline is a `list` with `ListAlignment::Bottom` and `FollowMode::Tail` (auto-scroll only when already at the bottom) **[V: `elements/list.rs`]**. If the user has scrolled up, the anchor holds, and a "Jump to latest · 12 new" pill appears.
- **Repaint rate.** Deltas arrive as fast as the host sends them. The app paints at most once per frame per window, and while a thread streams the drain task runs once per frame. The status pulse is a stepped 2 s duty cycle driven by a timer (04), never a per-frame animation, because GPUI's `with_animation` repaints every frame while it runs (research-look.md §6).

### 3.6 Timeline

Row kinds come from **structured events** (D4), never from regexes over prose. This replaces `pkg/remote/web/src/handoff.ts:22-30`:

| Row | Source event | Notes |
|---|---|---|
| User message | `MessageAppended(role=user, actor)` | Shows the author when it is a teammate (D8) |
| Agent prose block(s) | message parts | No container around agent prose (04) |
| Spec line | message `spec` + `spec_source` | Model and effort on every reply, with their source (thread, profile or default), per D5 |
| Actions row | tool-call parts | Tool runs folded into one row ("Read 6 files, ran 2 commands"). Expands in place. Tool calls are visible (D6) |
| "Worked for 4m" fold | `TurnEnd` | Collapses intermediate rows of a finished turn |
| Hand-off card | `HandoffCard` | from → to, brief, status, result, open the sub-thread |
| Task card | `TaskChanged` (delegated task) | Owner, delegate, status, result; Review and Accept when done (D6) |
| Plan card | `Plan` | Steps with state; opens as an artifact (D18) |
| Approval card | `ApprovalRequested/Resolved` | Names who may answer (role, D15). `y`/`n` keys |
| Question card | `Question` | Options inline |
| Subagent fold | `AgentChanged` with parent | Under the parent reply, with Follow and Open |

Selection: the timeline owns one selection model, an anchor and a head as `(row, block, byte offset)`. Rows render it through `StyledText` highlight ranges. Copy builds the text from `deck-state`, not from laid-out glyphs, so it works across rows that are not on screen.

### 3.7 Diff view (Changes tab and review)

- **Data.** The host computes the diff (git) and sends files and hunks with line numbers. The client computes word-level changes within a hunk with `similar` (MIT/Apache) on the background executor.
- **Rendering.** A `uniform_list` of diff lines: fixed-height rows, which is exactly what `uniform_list` is for ("measures the first element and then lays out all remaining elements", `elements/uniform_list.rs` **[V]**). A 20,000-line diff costs one screen of rows. Split view is the same list with two columns per row. Syntax colour comes from syntect, applied to visible lines only and cached per file revision.
- **Actions** (carried over from desktop-shell, with the D18 additions):
  - per-hunk Accept and Reject (host op);
  - revert a file with undo (`RevertFiles` / `UndoRevert`);
  - apply and discard an isolated branch, with a preview;
  - line comments that are drafted locally, persisted per thread, and **batched into the next message** to the agent that changed the line. A draft comes back if the send fails.

### 3.8 Terminal

- **Model.** Terminals are PTYs on the host (`TerminalOpen`, `TerminalWrite`, `TerminalResize`, `TerminalOutput` in `proto/deck/v1/deck.proto:68-72`). That works the same for the laptop and a VPS. The client is only the emulator and the renderer.
- **Emulator.** `alacritty_terminal` from crates.io (Apache-2.0). Zed uses its own fork pinned by git rev (`Cargo.toml:538` **[V]**); we use the upstream release.
- **Renderer.** Ours, in `deck-ui::TerminalView`, because Zed's `terminal_view` is GPL. It walks the damaged lines of the grid, groups cells into same-style runs, and shapes them with the GPUI text system. Rows are fixed height and only damaged lines are re-shaped. Estimate: 1.5–2.5k lines **[U]**.
- **Ownership.** A terminal belongs to a thread (or its worktree), and its pane position lives in that thread's layout. Closing a pane never touches another thread's shell (fixes B12, `layout/store.ts:88-103`).
- **Protocol ask.** `TerminalOutput` needs a byte offset and a scrollback replay, so a reconnecting client re-attaches without losing output. This is listed for the protocol chapter.

### 3.9 Agent browser (D10) and the Preview tab

The browser runs on the host: headless Chromium driven by the engine over CDP. Agents keep using the existing snapshot, outline and ref operations. The desktop is a **remote view**:

```
 host: Chromium ──CDP Page.startScreencast──▶ engine ──WatchBrowser(thread, tab)──▶ desktop
                                                       BrowserFrame{tab, seq, webp|jpeg, w, h, dpr, url, title}
 desktop ──BrowserInput{pointer|wheel|key|text(secret?)}──▶ engine ──CDP Input.*──▶ Chromium
```

- **Watching starts the screencast and leaving stops it.** The host stops when nobody watches, so an unwatched tab costs no frames. The frame rate adapts: up to 10 fps while the page changes, 1 fps when it is idle. The protocol chapter sets the exact limits.
- **Decoding.** `FrameView` decodes on the background executor (`zune-jpeg`, or `image` for WebP) into a GPUI image. A frame that arrives while the previous one is still decoding replaces it.
- **Outline.** The CDP outline is shown as an optional text column beside the frame. The TUI and the phone render the same outline.
- **Take control.** Pointer coordinates are scaled to page CSS pixels using `w`, `h` and `dpr`. Keys pass through while the frame has focus. Typing a password uses a **secure field**: masked, never logged, and sent as `text` with `secret = true`. The host injects it with `Input.insertText` and keeps it out of the transcript and the logs.
- **Sign-ins** live in the host-side profile directory, per project. The first open of the browser on a host says so in one line.
- **Origin approvals** and plan-mode gates already go through the engine path and appear as approval cards.

**Preview tab:** a host-Chromium tab pointed at the dev server's loopback port **on that host**, shown in the same `FrameView`. This works for remote hosts without exposing a port. On the local host, "Open in browser" opens `http://localhost:<port>` in the system browser. For a remote host, the button appears only when Deck Link offers a port forward (05).

What goes away: Electron `WebContentsView`s, the 8-live-page budget (`main/tabs.ts:16`), the desktop as `RegisterBrowserController` (`deck.proto:193`), and "paired remote Decks do not provide it".

### 3.10 Notifications, badge, deep links

- **Notifications.**
  - **When:** a thread enters Needs you (an engine-owned `InboxItem`). One notification per item, tagged with the item id so an update replaces it.
  - **Suppression:** when the window is focused and that thread is visible, and for hosts or projects the user has muted.
  - **Actions:** "Open". Approvals also get "Approve" and "Deny", but only for non-destructive approvals the user's role may answer (D15). Destructive ones open the app.
  - Uses GPUI `show_system_notification` with actions **[V]**.
- **Badge** = the Needs-you count across all hosts (§2.2, risk 11 for the per-OS code).
- **Deep links** (`deck://`):
  - `deck://pair?…` (device or host invite) and `deck://invite?…` (teammate) open a confirm sheet that shows the key fingerprint. They are never accepted silently.
  - `deck://open?host=<id>&thread=<id>` focuses the thread.
  - `deck://open?path=<folder>` keeps today's behaviour: it opens only after a confirm, because any web page can fire it (`main/launch.ts:34`).
  - The link formats belong to 05. The app registers the scheme as in §2.2, risk 11.

### 3.11 Keychain and token storage

- **What is stored where:**
  - The device's Noise static private key goes in the OS credential store via GPUI `write_credentials` / `read_credentials` (macOS Keychain, Windows Credential Manager, Secret Service via `oo7` on Linux) **[V]**.
  - Hosts' pinned public keys and roles live in the config directory. They are not secret, but they are integrity-relevant, so the file is 0600.
  - An optional Tailscale OAuth client secret (05) goes in the credential store.
- **Linux without Secret Service.** We don't silently write keys to disk (desktop-shell's rule, `main/servers.ts`). The app asks once whether to store the device key in a 0600 file. If the user says no, the device re-pairs each launch. Settings › Privacy shows which mode is active.
- **Never stored:** agent CLI credentials. Deck never reads them (D16).

### 3.12 Auto-update

- We use `cargo-packager-updater` against a signed JSON manifest on the GitHub release (`latest-desktop.json`). Its details are **[U]** until checked in M0.
- **Cadence:** check at launch and every 6 h. Download in the background, verify the signature, install on quit or through "Restart to update". Channels are Stable and Preview, and the GPUI app is Preview-only until M5.
- **Bundled binary:** the updater also replaces the bundled `deck` binary. It never restarts a running local daemon without asking ("3 agents are running on this machine. Restart the host now or later?").

### 3.13 Packaging

| OS | Artifacts | Notes |
|---|---|---|
| macOS | `.app` in `.dmg` and `.zip`, arm64 and x86_64 built separately | Developer ID signing and notarization from M5 (needs Apple secrets). Until then unsigned, as desktop-shell is today (`release.yml` comment "The apps are not code-signed"). |
| Windows | NSIS per-user installer `.exe` (no admin), x86_64; arm64 at M5 if CI allows | Registers `deck://` in HKCU and adds a Start menu entry. Authenticode via Azure Trusted Signing from M5. winget manifest after M5. |
| Linux | AppImage, `.deb`, `.tar.gz` (x86_64; aarch64 at M5) | `.desktop` file with `x-scheme-handler/deck`. Built on Ubuntu 22.04 for the glibc 2.35 baseline. No Flatpak: its sandbox fights spawning `ssh` and a user-level daemon. |

- **Tool:** `cargo-packager` for every format (MIT/Apache-2.0; DMG, NSIS, AppImage, deb) **[S]**.
- **Bundle contents:** the matching `deck` binary, so a fresh machine can start a local daemon.
- **Size target:** at most 45 MB compressed per installer, excluding the bundled `deck` (the engine binary is ~69 MB unstripped in the scratchpad build) **[U]**.

### 3.14 Crash reporting and logs (off by default)

- **Logs:** `tracing` with `tracing-appender` writes rolling files to the OS log directory and keeps 7 days. Logs never contain message bodies or secrets. Settings › About has "Copy diagnostics" (versions, OS, GPU adapter, path badges, the last 500 log lines).
- **Panics:** a panic hook writes a local crash file (backtrace, versions, OS, GPU adapter; no thread content).
- **Native crashes:** captured with `crash-handler` + `minidumper` (Embark, MIT/Apache) only when the user has opted in, because they need a small watcher process.
- **Sending:** off by default. After a crash, the next launch asks "Deck closed unexpectedly. Send the report?" and shows what will be sent. Settings › Privacy has "Send crash reports automatically" (off). There is no telemetry and no analytics.

---

## 4. Screen map

Visual styling, sizes and colours are in [04-design.md](04-design.md). This section fixes **structure, navigation and keys**.

### 4.1 Layout

```
┌──────────────────────┬──────────────────────────────────────────┬──────────────────────────┐
│ LEFT RAIL            │ MAIN                                     │ SIDE PANE (per thread)   │
│                      │                                          │                          │
│ ⌂ Home   3 · 5 · 2   │ Home = Inbox + Now                       │ [Changes][Tasks]         │
│   (needs you ·       │   Needs you   (approvals, questions,     │ [Agents][Browser]        │
│    running · review) │                blocked; answer in place) │ [Preview][Terminal]      │
│                      │   Review      (results, diffs, plans)    │                          │
│ AGENTS               │   Running     (live trees, all hosts)    │ Changes: files + diff    │
│  reviewer  ◆         │                                          │ Tasks:   board, budget   │
│  builder   ●         │ — or —                                   │ Agents:  delegation tree │
│                      │                                          │          Follow / Open   │
│ HOSTS                │ Thread view                              │ Browser: frames, outline │
│  laptop   local      │   header: title · project · host ·       │          take control    │
│  vps-1    tailnet    │           participants · branch          │ Preview: dev server      │
│  vps-2    offline 3m │   timeline (§3.6)                        │ Terminal: host PTY       │
│                      │   composer: agent·spec chip, mode,       │                          │
│ PROJECTS             │             attachments, queue/steer     │                          │
│  deck/api   on vps-1 │                                          │                          │
│   ● auth refactor    │                                          │                          │
│     └ ● tests (sub)  │                                          │                          │
│   ! db migration     │                                          │                          │
│  deck/web   laptop   │                                          │                          │
│   ✓ flaky test       │                                          │                          │
│  ▸ Settled           │                                          │                          │
└──────────────────────┴──────────────────────────────────────────┴──────────────────────────┘
 status bar: host paths · limits · update chip                     ⌘K command palette (overlay)
```

- **Left rail** (D7, D9, D18):
  - **Home:** Inbox and Now with three counts.
  - **Agents:** the persistent teammates roster with status (D6).
  - **Hosts:** the fleet with path badges and CLI login state (D16).
  - **Projects:** grouped by host, threads nested with their sub-threads, one status per thread in the 04 priority order, and a "Settled" fold.
  - Ordering within Projects follows 04 ("came back to you" first).
- **Main** is either Home or a thread. Settings, Usage and Add host are full-page views that replace Main; they keep the rail.
- **The side pane is per thread.** Each thread remembers its open tab and the pane's width. The tab set is fixed: Changes, Tasks, Agents, Browser, Preview, Terminal. That is 6, down from 9 (research-desktop.md §4). The Graph tab, Highlights, Work and Side chat are gone (§8.2). A side chat becomes an explicit sub-thread.
- **Command palette:** threads across hosts, message search, every action with its key, and "hand to…" (delegate).
- **Three levels of attention (D18):** a status indicator on the row, then **Follow** (pin a live preview of a sub-agent or thread into the side pane's Agents tab), then **Open** (go to it and talk to it).
- **Nothing remounts.** Switching thread or project changes which entity Main shows. Each thread's view state (scroll anchor, draft, side tab) lives in its entity or in `local.rs` (fixes B11, `ui/App.tsx:407-408`).

### 4.2 Keyboard first

GPUI key contexts and a keymap JSON with context predicates. Keys reach the focused element first, and there are no global capture listeners (fixes B17). Overlays form one **layer stack**: Escape closes the top layer only (fixes B16, `ui/kit/menu.tsx:47-49`, `ui/kit/dialog.tsx:27-31`). All bindings can be rebound in Settings › Keys. `mod` is Cmd on macOS and Ctrl elsewhere.

| Key | Action | Context |
|---|---|---|
| `mod-k` | Command palette | global |
| `mod-0` | Home (Inbox + Now) | global |
| `mod-1`…`mod-9` | Jump to the nth thread in the rail | global |
| `mod-j` | Next thing that needs you, across hosts | global |
| `mod-n` | New thread (in the focused project and host) | global |
| `mod-shift-m` / `mod-shift-e` | Agent and model picker / effort | composer |
| `mod-shift-d` | Hand to… (delegate to an agent profile) | thread |
| `enter` / `shift-enter` | Send (queued if busy) / newline | composer |
| `mod-enter` | Steer (atomic) | composer |
| `mod-.` | Interrupt | thread |
| `mod-\` | Toggle side pane; `mod-alt-1…6` picks a tab | thread |
| `` mod-` `` | Terminal tab | thread |
| `j` / `k`, `enter` | Move and open in lists (rail, Inbox, tree) | list |
| `y` / `n` / `a` | Approve / deny / always allow the focused approval | approval |
| `f` / `o` | Follow / Open the focused agent node | agents tree |
| `escape` | Close the top layer, then blur the composer | layer stack |

Every action is a GPUI `Action`, so it is reachable from the palette, the menu bar and the keymap. A button never owns logic the keyboard cannot reach.

### 4.3 The agent picker and delegation (D5, D6)

The look is in 04. Behaviour:

- **One picker.** It applies to an explicit **target**: the thread's lead, a specific member (by member id, never by kind), or a profile. It also has an explicit **scope**: this thread, or this profile.
- **Global defaults change only in Settings › Defaults.** The picker never writes them. This fixes `internal/workspace/agent_picker.go:149-171`.
- **The RPCs.** A pick calls `SetLead`, `SetMemberSpec` or (in Settings only) `SetDefaults`, each with a `client_op_id`. Each returns the resulting spec, which the chip shows. "Default" can always be chosen again.
- **The catalog** comes from one cached RPC per host and never blocks opening the picker. A stale catalog shows its age and refreshes in the background.
- **A pick applies when it is made.** There is no separate Apply button, so Escape never silently drops a choice (`ui/AgentPicker.tsx:176-178`).
- **"Hand to…"** (`mod-shift-d`, the palette, or the Agents tab) opens a small dialog: pick a profile (searchable, with each profile's description), write a brief, pick report-back (you, or the current agent). It creates a structured task (D6). Its card appears in the thread at once (optimistic), and the result returns as a reviewable card.

### 4.4 Components (`deck-ui`)

Each component has gallery entries for every state, light and dark, an accessible role and name, and a screenshot golden. Primitives wrap gpui-component, so we can replace them one at a time.

| Group | Components |
|---|---|
| Primitives (wrapping gpui-component) | `Button`, `IconButton`, `Input` (single and multi-line), `Checkbox`, `Switch`, `SegmentedControl`, `Tooltip`, `Popover` (anchored, deferred, flips vertically: fixes B15), `Menu` / `ContextMenu`, `Dialog`, `Sheet`, `Tabs`, `Scrollbar`, `Kbd`, `Avatar`, `Badge`, `Spinner` (stepped), `Toast` (coalescing), `Banner` |
| Layout | `AppShell` (rail, main, side pane, status bar), `Splitter`, `LayerStack`, `EmptyState` |
| Status | `StatusIndicator` (glyph + label, one per thread, 04 priority), `PathBadge`, `SpecBadge` (model + effort + source), `LimitMeter` |
| Rail | `RailSection`, `ThreadRow` (status, title, host, last line on hover), `HostRow`, `AgentRow`, `ProjectGroup` |
| Home | `InboxBand` (Needs you / Review / Running), `InboxItemRow` (with inline approve/deny), `NowTree` (live trees across hosts) |
| Thread | `ThreadHeader`, `Timeline`, `MessageBlock` (streaming Markdown), `ActionsRow`, `WorkedForFold`, `SubagentFold`, `HandoffCard`, `TaskCard`, `PlanCard`, `ApprovalCard`, `QuestionCard`, `JumpToLatest`, `PresenceStack` |
| Composer | `Composer` (queue/steer, `#` files, `/` commands, history, attachments), `AgentPicker`, `EffortControl`, `ModeControl`, `AttachmentChip`, `DelegateDialog` |
| Side pane | `FileList`, `DiffView` (unified/split, hunk actions, line comments), `TaskBoard`, `AgentsTree` (Follow/Open), `FrameView` (browser/preview), `BrowserOutline`, `SecureTextField`, `TerminalView` |
| Pages | `CommandPalette`, `SettingsPage` (Defaults, Agents/profiles, Hosts, Keys, Appearance: light/dark/system only, Privacy, About), `AddHostFlow`, `PairDeviceFlow` (QR for the phone, D19), `InviteFlow` (teammate, D15), `UsagePage` (simple), `HostOfflineBanner` |

---

## 5. Performance budgets

### 5.1 Budgets

These are measured on the reference machines in §5.3. "RAM" means the desktop process's resident memory (RSS, or private working set on Windows). It does **not** include the `deck` daemon or the agent CLIs, which are budgeted in the engine chapter.

| Metric | Budget (macOS) | Budget (Windows, Linux) | Fails CI when |
|---|---|---|---|
| Cold start to first painted window (cached summaries shown) | ≤ 400 ms | ≤ 600 ms | p50 over 10 runs exceeds the budget, or regresses > 10% vs `main` |
| Cold start to live summaries from a local daemon | ≤ 800 ms | ≤ 1,000 ms | same |
| Idle RAM, 0 threads | ≤ 120 MB | ≤ 160 MB | > budget, or > 5% vs `main` |
| Idle RAM, 10 threads (summaries; 1 open) | ≤ 150 MB | ≤ 190 MB | same |
| Idle RAM, 50 threads (summaries; 1 open, 3 warm) | ≤ 200 MB | ≤ 240 MB | same |
| Added RAM per 1,000 messages loaded in an open thread (text-heavy fixture) | ≤ 6 MB | ≤ 6 MB | same |
| Frame time while 5 threads stream (1 visible, 1 followed, 3 in summaries) | main-thread p99 ≤ 8 ms, p99.9 ≤ 16 ms | p99 ≤ 10 ms, p99.9 ≤ 16 ms | p99 > budget |
| Delta arrival → glyph on screen (visible thread) | ≤ 1 frame + network | same | p95 > 2 frames |
| Keypress → glyph in composer | ≤ 1 frame | same | p95 > 1 frame |
| Thread switch (warm) / (cold, local host) | ≤ 50 ms / ≤ 250 ms | same | p95 > budget |
| CPU at idle, nothing streaming, window visible | ≤ 0.5% of one core | ≤ 1% | mean over 60 s > budget |
| Diff of 20k lines: open and scroll | first paint ≤ 150 ms, scroll p99 ≤ 8 ms | same | > budget |

Why these numbers:

- **Zed-class apps start in 0.1–0.4 s** **[S]**. We do less work at start: no editor and no language servers.
- **The RAM floor is the GPU context plus font caches.** On Linux, wgpu's empty-window cost is reported at ~100 MB **[S]**, hence the higher non-macOS budgets.
- **Per-message cost stays small** because only visible rows hold laid-out text. The 1k-message number is mostly the strings themselves plus the parse cache.
- **Electron for comparison** (research-desktop.md §5, estimated): ~350–600 MB idle, plus O(n²) IPC per streamed reply.

### 5.2 How CI measures them

`tools/deck-bench` drives the real app binary in a bench mode:

1. It starts an in-process **fake host**: a tonic server that serves recorded `.deckrec` streams (§7.2) at the recorded pace, or faster.
2. It launches the app with `--bench <scenario>`. The app writes timestamps (process start, first frame, summaries live), per-frame timings (apply, layout, prepaint, paint: `tracing` spans exported as JSON) and an RSS sample every 250 ms to a results file.
3. Scenarios: `cold-start`, `idle-0`, `idle-10`, `idle-50`, `stream-5`, `history-1k` / `history-10k`, `switch`, `diff-20k`, `idle-cpu`.
4. `deck-bench compare` checks the results against the budgets and against the latest `main` baseline (stored as a CI artifact), and posts a table on the PR.

Where it runs:

- **Every PR (the gating job):** a macOS arm64 runner. A Linux runner with Mesa lavapipe (software Vulkan) and a Windows runner with the WARP software adapter run the same scenarios, but on those two we gate only **CPU-side and RAM numbers** and regressions vs `main`. Software rendering makes absolute GPU frame times meaningless. Whether GitHub's macOS arm64 runners expose a usable Metal device is **[U]**. If they don't, macOS frame-time gating moves to the nightly job and the PR job gates RAM and CPU-side time only.
- **Nightly:** absolute budgets on a self-hosted Mac mini (Apple silicon), a Windows box with a discrete GPU, and a Linux box (Intel iGPU, Wayland). Results go to a dashboard artifact. Two consecutive nightly failures open an issue automatically.

### 5.3 Reference machines

Each is the slowest machine we promise budgets on:

- macOS: an M1 MacBook Air, 8 GB.
- Windows: a 4-core laptop with an Intel Iris Xe iGPU and 16 GB.
- Linux: the same class of laptop on Ubuntu 24.04 under GNOME Wayland.

---

## 6. Carry-over and drop lists (from desktop-shell)

### 6.1 Carry over (ranked; parity at M5 means all of these)

Source: research-desktop.md §8.1, adjusted for D5–D10, D13 and D18.

| # | Feature (desktop-shell reference) | What changes in v3 | Milestone |
|---|---|---|---|
| 1 | Sidebar of threads across projects with one status model (`attention.ts` `roomStatus`), sub-thread nesting, Ctrl+1–9, jump to the next room | The status comes from the engine (`RunningSummary.state`), not computed in the client. Threads are grouped by host and project. `mod-j` replaces Ctrl+Alt+L. Needs-you clears on action, not on view (engine-owned). | M1 (rail), M2 (fleet-wide) |
| 2 | Timeline semantics: no container around prose, actions row (`actions.ts`), "Worked for…", subagent folds, hand-off and plan cards, jump to latest, virtualisation | Built from structured events (no `handoff.ts` regexes). Rows are blocks, and every list is virtualised (desktop-shell only virtualised above 200 rows, `ui/kit/virtual.tsx:4`). Model and effort are shown on each reply with their source. | M1 |
| 3 | Composer: queue vs steer, `#` files, `/` commands, history, images and video, approval and question card naming who may answer | Steer is atomic (`Send{steer}`). Attachments persist with the draft. The approval card shows the required role (D15). | M1 |
| 4 | Unified Inbox (cross-project, j/k/y/n, Always allow), OS notifications and badge | It becomes **Home = Inbox + Now** across hosts (D7). The bell and Highlights › Needs you are folded in. Notifications have actions. | M2 |
| 5 | A "Now" board (new; Work Graph model `graph.ts`, `design-glass.md` §7) | Live trees across all hosts from `WatchHost` summaries, with Follow and Open (D18). | M2 |
| 6 | A single agent picker with explicit targets | Target (lead, member id or profile) and scope (thread or profile) are explicit. Defaults change only in Settings (D5). | M1 |
| 7 | Delegation as a first-class verb; persistent named agents (profiles editor) | "Hand to…" creates a structured task with a card and a reviewable result (D6). The Agents roster sits in the rail, and the profiles editor uses the shared catalog. | M2 |
| 8 | Changes and diff: syntax colour, word diff, split view, revert with undo, line comments to the agent, apply and discard with preview | `uniform_list` diff. Line comments batch into the next message (D18). Per-hunk accept and reject. | M3 |
| 9 | Isolation: a thread on its own branch, apply with undo, discard; races (2–6 entrants plus judge) | Same ops. Races start from the palette or "Hand to…" with several profiles. | M3 |
| 10 | Terminal panes (xterm.js) and a per-room pane layout | `alacritty_terminal` with our renderer. Host PTYs, so terminals work on remote hosts too. The layout is per thread. | M3 |
| 11 | Palette (threads, message search, commands), git actions, ports chips, usage and limits, sharing roles | Palette across hosts. Git actions per project on its host. Sharing becomes Deck Link roles (D15). Usage stays small. | M1 (palette), M3 (git), M4 (roles) |
| 12 | Engine-stopped state with restart, window state, deep links, auto-update, logs | The engine-stopped state becomes a per-host offline state with a reconnect and path badge. A local daemon restart is offered only for the local host. | M0 (window, logs), M4 (host states), M5 (update, links) |
| 13 | Agent browser (`main/browser.ts`, `pageControl.ts`, `outline.ts`, `tabs.ts`) and Preview (`main/preview.ts`) | Host-side Chromium streamed as frames, with Take control and a secure text field (D10, §3.9). | M4 |
| 14 | Phone pairing from the desktop (`main/phone.ts`) and Pair with a Deck (`main/pair.ts`, `link.ts`, `pin.ts`) | One flow: Deck Link pairing (QR or link with fingerprint), and "Add a host" over SSH (D13, D16). | M4 |

### 6.2 Drop

| Drop | Reason |
|---|---|
| Art directions (Liquid Glass dither, Riso, Stitch), the backdrop, 18 MB of art, LookEditor aspects, WallpaperPicker, 10 palettes and 9 diff themes | D18. Light, dark and system only (04). |
| YoloFx, the app-icon picker, per-theme icons, launcher-icon drawing (`appIcon*`, `launcherIcon*`) | Decoration. One app icon. |
| Glass blur under live content | GPUI has no element backdrop blur, and it re-composites on every streamed repaint (research-desktop.md §5 GPU). |
| Notification bell centre and Highlights › Needs you | Folded into Home (D7). |
| Graph room view, Graph side tab, History graph | Replaced by the Agents tree (side pane) and Now. One placement. |
| Work tab (worktrees, capsules) | Worktree state shows on the thread header and in Changes. Capsules for messaging are deleted (D8). |
| Settings dock/float layout system (`resetGlobalLayout`) | One layout system: per-thread side pane (B13). |
| The v2 Controller / RpcClient / ProjectApi client triple, and the ~16 module stores | Replaced by `deck-client` + `deck-state`. |
| One `deck serve --rpc` per project (`main/decks.ts:249-252,410`) | One daemon per host (D3). |
| Regex parsing of hand-offs, plans and subagent folds | Structured events (D4). |
| Side chat as a hidden thread | An explicit sub-thread. |
| Lane strip "Steer" buttons that insert `$name` (`ui/Room.tsx:302-305`) | Dead verb. Delegation is "Hand to…". |
| Mode vs Level vs Workflow cards vs `/race` overlap; workflow prompt boilerplate in the user's turn (`workflows.ts:114-150`) | Mode stays (ask, plan, auto per 04). Orchestration is through agents and "Hand to…" (D6). Races stay as one verb. |
| Team page, team chat, contracts, shared mirrors (`SharedRoom.tsx`, `Team.tsx`, `TeamPost`, `ProposeContract`, `ReadShared`/`PostShared`) | D8. Teammates open threads directly with a role. |
| Analytics heat maps | A simple Usage page stays. |
| Electron WebContentsView browser, the 8-page budget, the desktop as browser controller | D10. |
| electron-updater, electron-log, safeStorage, cert-pinned WSS pairing | Rust equivalents (§3.11–3.14) and Deck Link. |

---

## 7. Testing

The rule from research-desktop.md §1.5: most bugs lived in the effectful glue (stores, streams, focus), which the old tests didn't reach. The plan aims the tests there.

### 7.1 `deck-state`: unit and property tests (no GPUI, fast)

- Apply rules: snapshot then deltas; a gap means resync from `last_rev`; deletions; tombstones; optimistic ops reconciled by `client_op_id`; rollback on error.
- **Property tests (`proptest`).** For any interleaving of snapshot fetch, deltas, a dropped connection and a resubscribe, the final state equals a straight replay of the log. This is the test that would have caught B2 and B3.
- Regression fixtures named after the old bugs: `b1_steer_is_atomic`, `b2_approval_survives_reconnect`, `b3_delete_during_gap`, `b5_failed_fetch_is_not_fresh`, `b6_background_roster_current`.

### 7.2 Replaying recorded event streams

- **Format: `.deckrec`.** A header (proto version, host features), then length-delimited `{t_offset, stream, proto event}` records. They are produced by `deck-client`'s recorder (dev flag) or by the daemon's `deck debug record --thread <id>` (protocol and engine chapters).
- **Corpus** (checked in, kept small with `zstd`, all from real daemons, with prompts scrubbed): long streaming reply with tool output; 5 parallel threads; delegation with a report back; approval during reconnect; history with 10k messages; 50-thread fleet summary; browser frames.
- The same corpus drives `deck-state` tests, the GPUI view tests (through the fake host), the perf harness (§5.2) and bug reports ("attach your `.deckrec`").
- desktop-shell's fakes (`test/fake-grpc.ts`, `test/fake-deck.cjs`) are mined for scenarios, then deleted with desktop-shell.

### 7.3 GPUI view tests (`TestAppContext`, `VisualTestContext`)

- **Deterministic tests.** They use GPUI's test platform: deterministic scheduling, `run_until_parked`, `simulate_keystrokes`, `simulate_input`, `simulate_click`, `simulate_window_resize` and simulated prompts and notifications (all present in `gpui/src/app/test_context.rs` **[V]**). They drive the real `deck-desktop` app with a fake host in-process.
- **What they cover:**
  - keyboard flows: palette, picker (arrow semantics), `y`/`n` on approvals, `mod-j` order;
  - the Escape layer stack (B16);
  - focus after thread switch;
  - drafts and attachments surviving a switch (B10);
  - no lost scroll anchor on project switch (B11);
  - terminal panes per thread (B12);
  - the picker never calling `SetDefaults` (D5);
  - a failed pick never sending (`ui/useRoom.ts:121-131`).
- **The a11y tree snapshot.** For each main screen, we serialise the AccessKit tree and diff it against a golden. Fork docs mention `Window::debug_a11y_tree_json` **[S]**. If upstream lacks it, we build the snapshot from our own role and name data in `deck-ui`.
- **No sleeps.** Time advances only through the test executor's clock. desktop-shell had 163 timeouts in `test/` (research-desktop.md §1.5).

### 7.4 Screenshot tests

- **How:** `VisualTestAppContext` renders with real Metal through off-screen windows and has `capture_screenshot` (`gpui/src/app/visual_test_context.rs:385` **[V]**). It is **macOS only**, so the screenshot job runs on the macOS runner.
- **Subjects:** every `deck-ui-gallery` entry (light and dark) and 12 full-screen states (Home with each band, thread streaming, picker open, diff split, terminal, browser frame, Settings, Add host, offline host).
- **Comparison:** perceptual diff with a small tolerance. Goldens are updated only in the same PR as an intended visual change, and the PR shows the before/after images.
- **Windows and Linux** get render smoke tests: launch, render Home and a thread from a `.deckrec`, check that a frame was presented and no errors were logged.

### 7.5 End-to-end against a real daemon (small and slow, on purpose)

At most 10 tests, run on Linux and macOS on every PR and on Windows nightly:

1. Start `deck serve` in a temp `HOME` with the engine's scripted test agent (no real model CLIs).
2. Launch the app in test mode, connected over the local socket.
3. Scenarios: connect and list; new thread and a streamed reply; approval round trip; "Hand to…" a profile and the result card comes back; daemon restart resumes without a resync (durable rev); terminal echo; browser frame arrives (headless Chromium on Linux CI); two daemons merged in Home with the correct host labels; teammate role caps an approval (D15).

---

## 8. Milestones to parity

The foundation (D12: protocol plus the engine fixes) lands first. M0 can start against a fake host generated from the draft v3 proto, and it switches to a real daemon as soon as `WatchHost`, `WatchThread` and `GetThread` exist.

| Milestone | Scope | Exit criteria |
|---|---|---|
| **M0: Skeleton, one host** | Workspace and crates. `deck-proto` codegen. `deck-client` on the local socket, plus SSH stdio for one remote. `deck-state` HostStore and ThreadStore with the gap and resync logic. App shell, rail (projects and threads from summaries), read-only thread view with streaming. Logs, window state, theme tokens from 04. CI: fmt, clippy, `cargo deny`, tests on 3 OSes, the first perf scenarios (`cold-start`, `idle-0`, `stream-5`). Spikes: tokio bridge, `gpui-pre` pin bump procedure, AccessKit API, `cargo-packager` output. | It streams a real thread from a local daemon within budget. Property tests pass. Unsigned installers build for 3 OSes in CI. |
| **M1: Threads, composer, picker** | Timeline row kinds (§3.6), Markdown with frozen blocks, selection and copy, composer (queue, atomic steer, `#`, `/`, history, attachments, drafts), approval and question cards, agent picker with target and scope (D5), spec on each reply, command palette, keymap and layer stack, new thread, interrupt, edit and resend, search. | Daily use on one host is possible. View tests for every B-bug fixture pass. The IME matrix has been done by hand. |
| **M2: Home and delegation** | Home = Inbox + Now (D7) across the hosts the user is connected to. Notifications with actions and the badge. Agents roster, profiles editor, "Hand to…", task and hand-off cards with reviewable results (D6). Agents tree with Follow and Open. | The Grok Bot flow works end to end: hand to the reviewer, get the result card, review and accept it. Home answers "what's running" in 0 clicks. |
| **M3: Changes, diff, terminal** | Changes tab, diff view (unified and split, word diff, hunk actions, line comments batched into the next message), isolation ops, races, git actions, terminal (`alacritty_terminal` with our renderer, host PTYs, per-thread layout), Preview stub (local only, system browser). | `diff-20k` and terminal perf are in budget. The review flow from 04 works. |
| **M4: Fleet and browser** | Deck Link paths in full (D14), path badges, Add a host over SSH (D16), host states (offline, update, CLI login), pairing and invite flows (D13, D15), roles in the UI, agent browser and Preview as host-Chromium frames with Take control and the secure field (D10), move a thread between hosts (D17). | Three hosts (laptop, 2 VPS) appear merged in Home. A browser task runs on a VPS and is watched from the desktop. |
| **M5: Parity, then delete desktop-shell** | Every carry-over item checked off (§6.1). Auto-update, deep links, signing and notarization, Windows and Linux polish, accessibility pass, final perf pass on the reference machines. A last desktop-shell release shows a "Deck has moved" banner with the download link. Then remove `desktop-shell/` and its CI and release jobs. | The owner signs off the parity checklist. Two weeks on the Preview channel with no P0 or P1 bugs open. All budgets green nightly. |

No calendar dates are given here. The delivery chapter sequences these against the protocol, TUI and phone work.

### 8.1 Transition and release changes

- **Both apps ship until M5.** desktop-shell stays on the Stable channel and gets fixes only, no new features. The GPUI app ships on the Preview channel from M0's first installer. Both read the same daemons. desktop-shell needs the protocol chapter's compatibility shim for as long as it ships.
- **`.github/workflows/release.yml`:**
  - Add a `desktop-gpui` job beside `desktop` (`needs: release`; matrix: `macos-14` arm64, `macos-13` x86_64, `windows-latest`, `ubuntu-22.04`). Its steps:
    1. checkout at the tag;
    2. set up Go (to build the bundled `deck`) and the pinned Rust toolchain;
    3. restore the vendored crates and the `sccache` cache;
    4. set the version from the tag in `desktop/Cargo.toml`;
    5. `cargo build --release -p deck-desktop`;
    6. build `deck` for the same target;
    7. `cargo packager --release`;
    8. sign the updater manifest (minisign key in `secrets.DESKTOP_UPDATE_KEY`);
    9. upload `Deck-<ver>-<os>-<arch>.<ext>` and `latest-desktop.json` with `gh release upload`.
  - Signing secrets (Apple, Azure Trusted Signing) are wired at M5. Until then the job sets no signing identity, matching today's `CSC_IDENTITY_AUTO_DISCOVERY: "false"`.
  - `publish` changes from `needs: [release, verify, desktop]` to `[release, verify, desktop, desktop-gpui]`. It still copies the CLI when a desktop build failed (`if: !cancelled() && needs.verify.result == 'success'`).
  - Artifact names must not collide with electron-builder's (`release/*.exe`, `*.dmg`, `latest*.yml`). The GPUI files therefore use the `Deck-…` prefix and `latest-desktop.json`.
  - At M5, the `desktop` (Electron) job and its `setup-node` and `npm ci` steps are deleted, and `publish.needs` drops `desktop`.
- **`.github/workflows/ci.yml`:** add a `desktop-rust` job, path-filtered to `desktop/**` and `proto/**`, which runs fmt, clippy `-D warnings`, `cargo deny`, the tests on 3 OSes, screenshots on macOS, and `deck-bench` with compare. The Electron CI job is removed at M5.
- **The installers** (`install.sh`, `install.ps1`) do not change for the CLI. The site's download page lists both desktop apps until M5, then only the GPUI app.

---

## 9. Open items handed to other chapters

| Item | Chapter |
|---|---|
| `TerminalOutput` with a byte offset and scrollback replay on re-attach | protocol |
| `WatchBrowser` / `BrowserFrame` / `BrowserInput` (with `secret` text) messages; screencast start and stop by watcher count; fps limits | protocol, engine |
| `Send{steer}` atomic op; `SetLead` / `SetMemberSpec` / `SetDefaults` returning the resulting spec; `spec_source` on each message | protocol (D4, D5) |
| A diff RPC returning hunks with line numbers; per-hunk accept and reject ops | protocol |
| `deck debug record` for `.deckrec` capture on the daemon | engine |
| Deck Link: local socket auth, Noise over each path, relay and SSH stdio framing, port forwarding for Preview, `deck://` link formats | [05-fleet-and-connect.md](05-fleet-and-connect.md) |
| Tokens, type scale, row heights, motion, status colours and glyphs, wireframe styling | [04-design.md](04-design.md) |

---

## 10. Sources

GPUI and the ecosystem (checked 2026-11-05):

- crates.io `gpui` API (0.2.2, 2025-10-22; Apache-2.0; repo zed-industries/zed) **[V]**: https://crates.io/api/v1/crates/gpui
- crates.io `gpui-pre` API (0.3.11, 2026-11-02, "snapshot of zed@f816e1f") **[V]**: https://crates.io/api/v1/crates/gpui-pre
- crates.io `gpui-component` API (0.9.0, 2026-11-02) **[V]**: https://crates.io/api/v1/crates/gpui-component
- GPUI README (pre-1.0, breaking changes, platform notes) **[V]**: https://github.com/zed-industries/zed/blob/main/crates/gpui/README.md
- GPUI Cargo.toml (version, licence, features incl. `inspector`) **[V]**: https://github.com/zed-industries/zed/blob/main/crates/gpui/Cargo.toml
- Zed workspace Cargo.toml (`[patch.crates-io]`, alacritty fork, accesskit versions) and `rust-toolchain.toml` **[V]**: https://github.com/zed-industries/zed/blob/main/Cargo.toml
- `gpui_linux` → `gpui_wgpu`; `gpui_wgpu` → wgpu; AccessKit adapters in `gpui_macos`, `gpui_windows`, `gpui_linux` **[V]**: https://github.com/zed-industries/zed/tree/main/crates
- `elements/list.rs` (`ListState`, `ListAlignment::Bottom`, `FollowMode::Tail`, overdraw) and `elements/uniform_list.rs` **[V]**: https://github.com/zed-industries/zed/blob/main/crates/gpui/src/elements/list.rs , https://github.com/zed-industries/zed/blob/main/crates/gpui/src/elements/uniform_list.rs
- `app/test_context.rs`, `app/visual_test_context.rs` (macOS-only screenshots), `platform.rs` (notifications, credentials, URL scheme, a11y callbacks) **[V]**: https://github.com/zed-industries/zed/tree/main/crates/gpui/src
- `gpui_tokio` (Apache-2.0, 2-worker runtime) **[V]**: https://github.com/zed-industries/zed/blob/main/crates/gpui_tokio/src/gpui_tokio.rs
- Linux renderer moved from Blade to wgpu, PR #46758, merged 2026-02-13 **[V: commit]**; memory and CPU figures **[S]**: https://github.com/zed-industries/zed/pull/46758 , https://git.secluded.site/zed/commit/af8ea0d6c26192c45f44f473c0d4a7d6f72ed018 , https://news.hada.io/topic?id=26669
- Zed on Windows, DirectX 11 renderer **[S]**: https://zed.dev/blog/windows-progress-report , https://www.neowin.net/news/windows-1011-users-have-a-reason-to-rejoice-as-microsofts-rival-gains-platform-support/
- GPUI accessibility via AccessKit (fork docs) **[S]**: https://docs.rs/crate/gpui-ce/0.3.0/source/src/window/a11y.rs , https://docs.rs/crate/rgpui/latest/source/src/_accessibility.rs
- IME: `InputHandler` / `EntityInputHandler` **[S]**: https://docs.rs/gpui/0.2.1/gpui/trait.EntityInputHandler.html , https://git.secluded.site/zed/commit/b9dce952072c26f0415c2da22247ebc326bd30b2
- Wayland IME candidate placement issues (ecosystem, not Zed-specific) **[S]**: https://github.com/fcitx/fcitx5/issues/985
- gpui-component / GPUI Kit README (components, AccessKit, Longbridge Pro, `gpui-pre` snapshots, Apache-2.0) and workspace Cargo.toml (`gpui-pre =0.3.11`) **[V]**: https://github.com/longbridge/gpui-component
- gpui-component webview (wry; macOS and Windows only; drawn over the window) **[S]**: https://docs.rs/crate/gpui-wry/latest/source/Cargo.toml , https://longbridge.github.io/gpui-component/docs/components/webview
- Non-Zed GPUI apps (awesome-gpui: Longbridge Pro, Codux, termy, hummingbird, zqlz, Loungy and others; star counts from an August 2026 mirror) **[S]**: https://github.com/zed-industries/awesome-gpui
- Zed terminal on `alacritty_terminal` **[V]**: https://github.com/zed-industries/zed/blob/main/crates/terminal/Cargo.toml
- `cargo-packager` and its updater **[S]**: https://github.com/crabnebula-dev/cargo-packager , https://docs.crabnebula.dev/packager/
- Zed and Electron/Tauri RAM and start-up figures **[S]**: see research-external.md §3.1–3.2

In-repo evidence: research-desktop.md (bugs B1–B19, carry-over §8.1, drop §8.2, hard-to-move §8.5), research-engine.md (§A1–A10, §3, §5), research-look.md §6, `proto/deck/v1/deck.proto`, `pkg/remote/e2e/e2e.go`, `.github/workflows/release.yml`, `desktop-shell/src/main/*`.

Could not open from this session (network policy): docs.rs, gpui.rs and longbridge.github.io pages directly. Facts attributed to them come from search snippets **[S]**.
