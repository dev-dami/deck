# 05. Deck Link: hosts, devices and teammates

Status: plan, 2026-11-05. Decisions D3, D8, D9 and D13–D17 (chapter 00) are
settled; this chapter turns them into a design. Wire framing inside the
encrypted channel belongs to chapter 07; the deletions this chapter relies on
are listed in chapter 02.

**In one paragraph.** Every machine that runs the `deck` daemon is a
*host*. Every desktop, TUI and phone is a *device* that belongs to a
*person*. A person reaches a host over whichever path works (the local
socket, your tailnet, a direct address, a Noise relay you can run yourself,
or an SSH pipe), and the host lets them in only if their device key is
pinned there and their role allows the call. There is one key per host and
one per device, one pairing flow, one channel and one API. Teammates get
direct access to the host's threads with a role. There is no chat, outbox
or contract. We build this on the phone's v3 stack in `pkg/remote`, which
already has Noise channels, pinned device keys, an offline queue, passkeys
and push. We do not build it on the peer link.

Contents:
1. The model
2. Paths and transport
3. Flows, step by step
4. Roles and approvals
5. What it replaces, and the migration
6. Build order and open questions
7. Sources

---

## 1. The model

### 1.1 Nouns

| Noun | What it is | Identity | Where it lives |
|---|---|---|---|
| **Host** | Any machine running the daemon (`deck host run`): your laptop, a VPS, a home server. It serves every project checked out on it, and its threads keep running with no client attached (D3). | One host key pair (§1.2), shown as a fingerprint and a short name (`vps-1`). The `cid` is derived from the key. | `~/.deck/remote/identity.json` (the current v3 file, `pkg/remote/identity/identity.go`) |
| **Device** | One client install on one machine user: the GPUI app and the TUI on your laptop share one device, and your phone is another. | One X25519 Noise static per device. It never leaves the device. | Desktop and TUI: the link agent's key file (§1.5). Phone: SecureStore (built). |
| **Person** | A human. The host owner is a person; each teammate is a person. A person has one or more devices. | A person key (Ed25519). The **member id** is its fingerprint. Grants are keyed by the member id, never by a display name (D15). | On the person's first desktop or TUI device, in the OS keychain. It is copied only to devices the person marks "can add devices and accept invites". |
| **Member** | A person who holds a grant on a host. | member id + display name (a label only) | The host's member store (§1.3) |
| **Grant** | A role on a scope: the whole host, or one project on it. | `(member id, scope, role)` | The host's member store |
| **Role** | `viewer`, `member` or `admin` (§4). The host owner is always an admin and cannot be removed. | | |
| **Project** | A repository, keyed by its root commit, as `memory.*` and leases already key it. One project can be checked out on several hosts. The path never leaves the host (`pkg/remote/projects`). | `pj` | The host's project registry |
| **Thread** | Runs on exactly one host at a time. Its global id is `(host cid, thread id)`. Every client shows where it runs: "on vps-1". | | The host's thread store (chapter 07) |

Two rules hold everywhere:

- **A connection is a device, a call is a person.** The host authenticates
  the Noise static, maps it to a device, maps the device to a member, and
  checks that member's role on every call. Every write carries the member
  as `author`, so messages render as "ana", not as "[vps] …".
- **The network is never the trust boundary.** Being on the same tailnet,
  LAN or SSH account grants nothing on its own (§2.5).

### 1.2 Keys

| Key | Algorithm | Purpose | Exists today? |
|---|---|---|---|
| Host relay key | Ed25519 | Proves the host owns its `cid` to a relay (`"deck-relay-v1|cid|nonce"`, `docs/mobile-contract.md` §5) | Yes, `pkg/remote/identity` |
| Host static | X25519 | Responder static of every Noise IK session; pinned by every device | Yes, same file |
| Device static | X25519 | Initiator static; pinned by every host the device may reach | Yes for phones (`devstore.Device.Static`, `pkg/remote/devstore/devstore.go:63-78`) |
| Person key | Ed25519 | Binds "the same person" across hosts and invites; signs join requests and "add my device" requests | **New** |
| Passkeys | WebAuthn ES256/EdDSA | Fresh user verification for destructive approvals and yolo (`docs/mobile-contract.md` §7) | Yes, per device |

What the person key does and does not do: a host never accepts a
*connection* on the person key alone. It only accepts pinned device statics.
The person key lets a host check that a join request or a new device really
comes from the member it claims to be, so a teammate who accepts invites to
two hosts is the same member on both, and a re-pair under a reused display
name inherits nothing. That fixes today's name-keyed shares
(`t.shares[peer]`, `internal/deck/session/share.go:115-130`).

### 1.3 The host's stores

`devstore` grows into a member and device store. It stays one JSON file per
host with the existing rules: read-only `Verify`, last-seen written at most
once a minute, one revocation watcher per process (`docs/remote-contract.md`
R16).

```
~/.deck/remote/
  identity.json      host relay key + host static            (exists)
  members.json       [{member_id, person_pub, name, grants:[{scope, role}],
                      added_by, added_at}]                    (new)
  devices.json       [{device_id, static, member_id, name, kind:
                      desktop|tui|phone, relay_token_hash, allow_yolo,
                      passkeys[], push?, last_seen, expires_at}]  (exists; gains member_id, kind)
  revoked.json       device statics and member ids revoked here (new)
  audit.jsonl        joins, role changes, device adds/removals, approvals
                     answered by non-owners, moves, Sign in sessions (new; the peer
                     audit file is retired)
  tsnet/             embedded Tailscale node state, 0700       (new, only with tsnet)
```

On first start after the upgrade, every existing phone in `devices.json`
becomes a device of the owner member, so paired phones keep working (§5.3).

### 1.4 How this maps onto `pkg/remote`

The v3 phone stack already is a multi-host design: phones keep a
`FleetState` of computers (`pkg/remote/web/src/state3.ts:119`, reused by
`apps/mobile/src/core/client.ts`), projects are addressed by `pj`, and the
daemon is always on. Deck Link generalises it from "phone ↔ PC" to "any
device ↔ any host".

| Part | Today | Deck Link | Verdict |
|---|---|---|---|
| `identity` (135 lines) | Computer keys | Host keys, unchanged | **Reuse** (drop its import of `internal/deck/peer`, `identity.go:19`; chapter 02, step 0) |
| `e2e` (266) | Noise IK/IKpsk2/K, chunking, rekey | Same channel for every device kind and every path | **Reuse** |
| `daemon/pair.go` (233) | One IKpsk2 slot, 5 min, burned after 3 failures | Same slot, armed for an *invite* (role, scope, sealed bundle) or a *device of an existing member* | **Generalise** |
| `devstore` (440) | Phones | Devices of members, plus `members.json` | **Generalise** |
| `daemon/gate.go`, `policy` | Tier classifier, passkey gate | Role check per call, then the tier gate | **Generalise** (add roles in front) |
| `daemon/queue.go`, `queue` | Sealed queued actions, receipts, veto window | Same, for any device; keyed by member for attribution | **Reuse** |
| `daemon/snapshot.go`, `push.go`, `push` | Sealed snapshots and E2E web push for phones | Same; snapshots now carry the host summary (chapter 07) | **Reuse** |
| `relayclient` (472) | Host side of `relay/v1` | Unchanged; lanes open for any device | **Reuse** |
| `relay` (TypeScript Worker, 4 files) | The only relay | Kept as the optional hosted relay; `deck relay` (Go) implements the same `relay/v1` contract | **Keep + add a Go twin** |
| `wire` v3 `Frame3` | Hand-written JSON mirror of v2 methods | Replaced by `deck.v2` Link frames inside the channel (chapter 07); `QueuedAction` and `Snapshot` stay as sealed payloads | **Replace** |
| `ipc`, `mobile`, `adapter` | Per-project Deck processes provide to the daemon | Gone: one daemon serves every project (D3) | **Delete** with chapter 07's daemon |
| `server`, `auth` (v2) | `deck remote` HTTPS + WebSocket | Gone | **Delete** (chapter 02) |
| `internal/mobilecontrol` | Supervises a local Node/wrangler relay for LAN mode (`docs/mobile-lan.md` §2) | Gone: `deck relay` is a Go subcommand, and the LAN case is the direct path | **Delete** |
| `autostart` | systemd user unit, LaunchAgent, Scheduled Task | Used by `deck host up` and Add a host | **Reuse** |

Rough size: Deck Link adds 4–6k lines and deletes about 32k (peer link,
team, contracts, vsh) plus 2–3k (v2), per research-team §5.4.

### 1.5 The client side: one link agent per machine user

The GPUI app is Rust and the phone is TypeScript; tsnet, the SSH carrier
and path racing are Go. We write the path logic once:

- **Desktop and TUI** talk plain gRPC (`deck.v2`) to a **link agent** on a
  local Unix socket (`~/.deck/run/link.sock`, 0600 in a 0700 directory).
  The agent is the `deck` binary (`deck link agent`), started on demand by
  either client, like `ssh-agent`. When the laptop's own daemon runs, the
  agent runs inside it. The agent holds the device static, the host list,
  the pins, the per-host outbox and cache, and every path. It terminates
  Noise and re-exposes each host as a gRPC target selected by a `host` call
  header.
- **The phone** keeps its own Noise client in TypeScript (built and
  vector-tested against Go, `docs/mobile-contract.md` §3) and uses the
  relay, direct and tailnet paths, never SSH and never tsnet.
- **A host is never a client of another host.** Moving a thread (§3e) is
  driven by an admin's link agent, which streams the export from the
  source host into the target host. That keeps host-to-host trust out of
  v3 entirely.

```
 GPUI app ─┐  gRPC over link.sock                       ┌─ host "laptop" (local socket, OS user = owner)
 TUI ──────┴─▶ link agent (deck) ──Noise IK over──────── ├─ host "vps-1"   (tailnet · direct 18 ms)
                 device static,     tailnet | direct |   └─ host "home"    (relay via vps-1)
                 pins, outbox,      relay | SSH stdio
                 cache, paths
 phone (Expo) ── Noise IK over relay | direct | tailnet ──▶ same hosts
```

---

## 2. Paths and transport (D14)

### 2.1 The five paths

All remote paths carry the same thing: Noise IK transport messages, chunked
as in `docs/mobile-contract.md` §3, framed as WebSocket binary messages on
network paths and as length-prefixed frames on SSH stdio. One framing
means one set of fuzz and hostile tests.

| # | Path | How the host listens | How a client dials | Who uses it |
|---|---|---|---|---|
| 1 | **Local socket** | `~/.deck/run/host.sock`, 0600 in 0700; no Noise. The OS user *is* the host owner: anyone who runs as that user can already read `~/.deck`. | The link agent, when the host is this machine | Desktop and TUI on the same machine |
| 2 | **Tailnet** | TCP 7743 on the Tailscale address only. System `tailscaled` first (address from the LocalAPI); else an embedded tsnet node `deck-<host>` (`srv.Listen("tcp", ":7743")`). | MagicDNS name (`vps-1:7743` or `deck-vps-1:7743`) via the system Tailscale client; the phone needs the Tailscale app | Your own devices and teammates who share a tailnet |
| 3 | **Direct** | TCP 7743 on private LAN addresses (on by default) and on a public address (off by default; turned on in the network step) | Last-known addresses from the invite and from the host's `HostInfo` | Phone on home Wi-Fi; a VPS with a public IP |
| 4 | **Noise relay** | Outbound WebSocket from host to relay (`relay/v1`, built). The relay is `deck relay` on a box you own (often the VPS itself), or the optional hosted relay. | `wss://relay.example.com/r/v1/c/:cid/dev` | Phones and teammates with no tailnet and no direct reach; also carries the offline mailbox, snapshots and push |
| 5 | **SSH stdio** | Nothing new listens. `deck link --stdio` on the host is a pipe into the daemon's link listener. | `ssh <alias> ~/.local/bin/deck link --stdio`, run with the user's own OpenSSH, config, agent, FIDO keys and `ProxyJump` | Bootstrap (Add a host), rescue, and hosts set to "SSH only"; never a teammate path (it would need a Unix account) |

Pre-auth limits from `docs/remote-contract.md` R4 apply to every listening
path: 4 KiB and 5 s before the first Noise message completes, at most 4
unauthenticated connections per source and 32 in total, limiter state
pruned on a timer.

### 2.2 How a client picks a path

The link agent (and the phone's client) keeps one live channel per host and
races to get it:

```
connect(host):
  if host is this machine: use the local socket; done
  t=0     dial tailnet (MagicDNS) and every known direct address, in parallel
  t=300ms if none finished the Noise handshake: also dial the relay
  t=3s    if still nothing and the host has an SSH alias: also dial SSH stdio
  keep the first channel that completes Noise IK with the pinned host static;
  close the rest
  resume every open subscription from its last revision (chapter 07)
while connected on relay or SSH:
  every 30 s, and on any network change, probe tailnet and direct in the
  background; if one answers, open it, resume, then drop the worse path
on loss: retry with backoff 1 s → 30 s (FleetState.schedule today), same race
```

- A host the user set to "SSH only" skips straight to path 5 and reuses one
  SSH connection (`ControlMaster`-style) for all its streams.
- A switch of path is invisible to the user except for the badge: resume is
  by durable per-thread revision, so a reconnect replays from the log and
  never refetches the whole thread.
- Latency is a Noise-level ping every 15 s on the live channel, shown as a
  rolling median.

### 2.3 What the UI shows

Every host row has one **route badge**, the same words on desktop, TUI and
phone:

| Badge | Meaning |
|---|---|
| `local` | This machine |
| `tailnet · direct 18 ms` | Tailscale with a direct WireGuard path |
| `tailnet · relayed 84 ms` | Tailscale via DERP or a peer relay (from `tailscale ping`-style status on the LocalAPI or tsnet's local client) |
| `LAN 4 ms` / `direct 31 ms` | Direct TCP to a private or public address |
| `relay vps-1 62 ms` | Through `deck relay` on vps-1 (or `relay hosted`) |
| `SSH 40 ms` | SSH stdio pipe |
| `offline 3m` | No path; the cached view is shown greyed (§3f) |

Hovering (desktop) or long-pressing (phone) the badge shows every path tried,
its result and its latency, so "why is this slow" has an answer.

### 2.4 Tailscale: system, embedded, Headscale

**System `tailscaled` first.** If the host's Tailscale LocalAPI answers, the
host listens on its Tailscale address and uses the machine's MagicDNS name.
There is no extra tailnet device, no binary cost, and the user's ACLs and
Tailscale SSH apply. Deck never runs `tailscale serve` or `funnel` on the
user's behalf, and refuses to touch an existing Serve config (`docs/remote-contract.md`
R13).

**Embedded tsnet when there is no system Tailscale** (containers, no root, a
box where the user does not want a daemon). tsnet runs a full Tailscale node
inside the process on a userspace network stack, with state in a directory
we choose, and reads its auth from `AuthKey`, `TS_AUTHKEY`, or prints a
login URL when it has neither (https://pkg.go.dev/tailscale.com/tsnet). The
host joins as its own node `deck-<host>` with state in `~/.deck/tsnet/`
(0700). Throughput on the userspace stack is lower than kernel WireGuard,
which does not matter for Deck's traffic.

**Headscale for fully self-hosted.** Settings › Network › Control server:
"Tailscale" (default) or "Headscale URL". Deck passes the URL as tsnet's
`ControlURL`, or tells the user to run `tailscale up --login-server=<url>`
for system Tailscale (https://pkg.go.dev/github.com/juanfont/headscale;
version and admin model from https://www.infralovers.com/blog/2026-05-22-headscale-self-hosted-tailscale-alternative/,
*secondary*). Headscale has one tailnet and a CLI only, so the join flow
shows the node's registration key and the exact `headscale nodes register …`
command; if the Headscale box is itself a Deck host, an admin can run it from
the Sign in terminal. Pair it with a self-hosted DERP (`derper`) for zero
third-party relays.

**MagicDNS names.** A host's display name defaults to its MagicDNS short name
when it has one, so the name you see in Deck is the name you can `ssh`.
Names are labels; the pinned key is the identity.

### 2.5 Security

**The trust model.**

```
reachability (any path)  ──▶  Noise IK: host static pinned by the device,
                              device static pinned by the host (not revoked,
                              not expired)
                         ──▶  device → member → role on this host/project
                         ──▶  per-call check: role, then approval tier
                              (pkg/remote/policy), then passkey if destructive
```

**Why network membership is never enough.**

- Tailnet ACLs are often "allow all", tagged servers are shared across a
  team, and a shared node is visible to people outside your organisation
  (https://tailscale.com/docs/features/sharing). Treating "on the tailnet"
  as "trusted" would give every device on it the host's agents and its CLI
  logins.
- One code path: users without Tailscale get exactly the same checks.
- Tailnet identity is at most an **extra** check. An admin can set "expect
  tailnet user `ana@…`" on a member; the host then also checks
  `WhoIs(remote addr)` (tsnet in process, system Tailscale via the LocalAPI)
  and refuses a mismatch.
- If a tailnet policy grants an app capability for Deck (a JSON capability
  under a domain we own, attached to a src→dst grant, read from
  `WhoIs(...).CapMap`; https://tailscale.com/docs/features/access-control/grants/grants-app-capabilities)
  and a Deck role also applies, **the lower one wins** (D13). The capability
  never raises a role and never admits an unpinned device.

**Auth keys for tsnet.**

1. **Default: interactive.** The tsnet node on the host prints its login URL;
   over the bootstrap SSH session Deck shows it on the desktop and opens the
   browser. The user approves in Tailscale's own admin page. No secret passes
   through Deck.
2. **Fleets: an OAuth client** (scope: auth keys, tag `tag:deck`) stored in
   the owner's desktop OS keychain, never on a host. Per host, Deck mints a
   **one-off, tagged, pre-approved, non-ephemeral** key and writes it to the
   host over the SSH session's **stdin**, where it is used once and dropped.
   Tagged nodes have key expiry off by default
   (https://tailscale.com/kb/1085/auth-keys).
3. **Never a reusable key in a script, cloud-init, argv, environment or a
   log.** `deck host up` reads a key only from stdin
   (`--tailscale-authkey -`) or a 0600 file it deletes after use, refuses
   one in argv, and redacts `tskey-` strings from its logs. Our docs show
   only one-off keys. Cloud-init may carry a short-lived Deck invite, never a
   Tailscale key.
4. **Revoking a host:** delete the node in Tailscale (or Headscale) and
   remove the host in Deck; Deck does both from one button when it holds the
   OAuth client.

**Other rules.**

- **Bootstrap never touches model credentials.** Deck installs itself, not
  CLI logins (§3g).
- **SSH host keys:** first contact uses the system `ssh` and its
  `known_hosts`. For a new host Deck shows the fingerprint with "compare
  with your provider's console".
- **Pairing secrets** in QR codes and links are single-use, expire in 5
  minutes, are burned after 3 failures and bind to the host's pinned
  fingerprint (built: `docs/mobile-contract.md` §4).
- **Install integrity:** `install.sh` checks SHA-256 against
  `checksums.txt` today (`install.sh:87-96`). Add a signature check
  (minisign or cosign) before we tell people to pipe it into a shell on a
  server. Add a host avoids the question by pushing the desktop's own
  binary.
- **Blast radius:** a worktree per thread. Members from different people
  get separate worktrees. Grok Bot's documented weak point is that all of
  an account's bots share one computer, cookies and CLI credentials
  (https://www.unite.ai/xai-launches-grok-bot-always-on-ai-teammates-with-their-own-cloud-computers/,
  *secondary*); Deck's per-host roles and per-thread worktrees are the
  answer, and the invite tells teammates whose machine and logins they use.

### 2.6 Binary size

- Today's `deck` binary is 68.9 MB unstripped (research-tui, measured).
- tsnet pulls in WireGuard, the gVisor netstack and the Tailscale client.
  Tailscale's own client plus daemon is about 18–23 MiB unstripped
  (https://tailscale.com/kb/1207/small-tailscale/). tsnet inside a mobile
  app has been measured at about 23 MB on device, about 14 MB after store
  compression (https://pub.dev/documentation/tsnet_flutter/0.3.0, *secondary*).
  **Plan for +10–20 MB** on `deck`, and measure with the real flags.
- Trim with the `ts_omit_*` build tags and measure each with `omitsize`
  (https://pkg.go.dev/tailscale.com/cmd/omitsize). CI fails if tsnet adds
  more than 20 MB to the stripped release binary.
- **Default build includes tsnet**; it is what makes a fleet work in two
  minutes on a box with no Tailscale.
- **`deck-slim`** (`-tags notsnet`) is published next to it for people who
  run system Tailscale or none. It has every other path. `deck host up`
  on a slim build offers system Tailscale, direct, relay and SSH only.
- **The GPUI app never links tsnet** (the link agent is the `deck` binary).
  **The phone never embeds tsnet** in v3 (§6.2).

---

## 3. Flows, step by step

Each flow has a time budget measured from the first action to the result,
on a normal connection. Times exclude the user reading.

### (a) Add your own VPS

**Budget: under 2 minutes for either path**, excluding a first-time Tailscale
login in the browser.

#### CLI path: `deck host up` (on the VPS)

```
$ curl -fsSL https://raw.githubusercontent.com/dev-dami/deck-releases/main/install.sh | sh
deck 3.0.0 installed to ~/.local/bin/deck (sha256 ok)                 ~20 s

$ deck host up
Host       vps-1  (fingerprint SHA256:7Qx2…kP)
Service    ~/.config/systemd/user/deck.service   enabled, running
Linger     on   (survives logout)
Projects   ~/src/api  ~/src/web                  (git repos under ~)
Network    Tailscale found: vps-1.tail1234.ts.net  → listening on :7743
           Public 203.0.113.7: off   (deck host net --public on)
Relay      none  (deck relay setup, or use a relay you already run)
CLIs       claude  ✓ signed in    codex  ✗ not signed in → deck host signin codex

Link a device (expires in 5:00, one use):

   █▀▀▀▀▀█ ▄▀▄ █▀▀▀▀▀█
   █ ███ █ ▀█▄ █ ███ █        scan with Deck on your phone, or paste on desktop:
   █ ▀▀▀ █ █▀▄ █ ▀▀▀ █        deck://join?v=4&p=eyJ2Ijo0LCJjaWQiOi…
   ▀▀▀▀▀▀▀ ▀ ▀ ▀▀▀▀▀▀▀

Waiting… "Dami's MacBook (desktop)" joined as admin.  ✓              ~10 s
```

What `deck host up` does, in order:

1. Creates or loads the host identity.
2. Writes `~/.config/systemd/user/deck.service`, generated from
   `docs/examples/deck-serve.service`: `ExecStart=%h/.local/bin/deck host run
   --drain 2m`, `KillMode=mixed`, `TimeoutStopSec=150`, `ExecReload`, the
   hardening block, and `ReadWritePaths` computed from `~/.deck`, the CLIs'
   folders and the registered projects. It drops the "turn peers on" and
   `deck peer pair` text and the exclusive thread lock: a TUI on the VPS
   attaches to the daemon as a local client.
3. Runs `loginctl enable-linger $USER`. If that needs root it prints the one
   `sudo` line and carries on, warning "stops at logout until linger is on".
4. Registers git repositories under `~` (or `--projects <dir>…`) as projects.
5. Detects the network: system Tailscale → listen on its address. No
   Tailscale → asks "Join your tailnet as deck-vps-1? [Y/n]" and prints the
   tsnet login URL (or reads a one-off key from stdin with
   `--tailscale-authkey -`). Headscale: `--control-url https://hs.example.com`.
6. Probes the coding CLIs on PATH and their sign-in state (exit code only).
7. Arms one pairing slot whose first joiner becomes the **owner** (only on a
   host with no owner yet) and prints the QR and link. The link carries
   every route (`ts`, `lan`, `pub`, `relay`, `ssh` hints).

`deck host up` is idempotent: run again, it repairs the unit and shows a
fresh link for an *additional device of the owner* (never a second owner).

#### Desktop path: "Add a host" over SSH

```
┌ Add a host ──────────────────────────────────────────────────────────┐
│ From ~/.ssh/config                                                   │
│   ● vps-1          dami@203.0.113.7                                  │
│   ○ home           dami@home.lan (ProxyJump bastion)                 │
│   ○ Other…         user@address                                      │
│                                                                      │
│ New host key for vps-1:                                              │
│   ED25519 SHA256:Hk3w…9s   compare with your provider's console      │
│                                         [ Cancel ]  [ Trust and add ] │
└──────────────────────────────────────────────────────────────────────┘
```

1. **Pick the box** (5 s). Concrete aliases from `~/.ssh/config` (not
   `Host *`), or `user@address`. Deck runs the system `ssh`, so `ProxyJump`,
   agents, FIDO keys and `known_hosts` all work. This is the pattern Zed,
   VS Code, JetBrains Gateway, DevPod and the Codex app share
   (https://zed.dev/docs/remote-development, https://devpod.sh/docs/how-it-works/overview;
   Codex from https://codex.danielvaughan.com/2026/05/21/codex-remote-connections-mobile-pairing-ssh-hosts-enterprise-access-tokens/,
   *secondary*).
2. **Probe** (2 s): `uname -sm`, systemd and linger, an existing `deck` and
   its version, Tailscale LocalAPI, port 7743 free, CLIs on PATH and their
   sign-in state.
3. **Install** (10–30 s): the desktop pushes the `deck` binary of **its own
   version** for the host's OS and arch over the SSH session into
   `~/.local/bin/deck` (no root, no egress needed on the host, no version
   skew; Zed's `upload_binary_over_ssh` pattern). Fallback: the release
   download with checksum check. Then `deck host up --json --no-qr`, which
   installs the unit exactly as above. Deck never relies on the remote
   login shell's PATH: it calls `~/.local/bin/deck` by full path (Codex's
   `.bashrc` PATH trap, *secondary*).
4. **Pin both ways over SSH** (1 s): the desktop runs
   `~/.local/bin/deck link --stdio` on the same SSH session and completes
   the IKpsk2 handshake with a slot armed by `host up`. The host pins the
   desktop's device static and person key as the owner; the desktop pins
   the host static. No code to type: SSH is a stronger channel than a
   6-digit code.
5. **Network** (one screen, 5 s plus any Tailscale login):

```
┌ How should your devices reach vps-1? ───────────────────────────────┐
│ ● Tailscale is running on vps-1  (vps-1.tail1234.ts.net)  Recommended│
│ ○ Join your tailnet as deck-vps-1   (embedded; opens a login page)   │
│ ○ Public address 203.0.113.7:7743   (pinned keys only)               │
│ ○ SSH only   (Deck connects through ssh when you open it)            │
│                                                                      │
│ ☑ Also run a relay on vps-1 for your phone and teammates             │
│   (needs a domain or a public IP; deck relay)                        │
│                                                       [ Continue ]   │
└──────────────────────────────────────────────────────────────────────┘
```

6. **CLI logins** (§3g): the card shows what is signed in, with Sign in.
7. **Done.** The host appears in the fleet rail with its route badge and
   "Start a thread here".

If any step fails, the dialog shows the step, the command that failed and
its stderr, and "Retry" or "Open a terminal on vps-1". Re-running Add a host
on a host that already runs Deck upgrades or repairs it in place.

### (b) Connect your phone: one QR, every host

**Budget: 15 seconds** from "Link a phone" to the phone showing every host.

```
┌ Link a phone ────────────────────────────────────────────────────────┐
│   █▀▀▀▀▀█ ▀▄█▀ █▀▀▀▀▀█     This phone will reach:                    │
│   █ ███ █ ██▄▀ █ ███ █       laptop   relay vps-1                    │
│   █ ▀▀▀ █ ▄▀█▄ █ ▀▀▀ █       vps-1    tailnet · relay vps-1          │
│   ▀▀▀▀▀▀▀ ▀ ▀▀ ▀▀▀▀▀▀▀       home     relay vps-1                    │
│                                                                      │
│   Expires in 4:52 · one use · pairs through vps-1                    │
│   [ Copy link ]                                                      │
└──────────────────────────────────────────────────────────────────────┘
```

How it works:

1. The desktop picks a **pairing host**: the always-on host with a relay
   (here vps-1), else the laptop.
2. Its link agent calls `ArmDevicePair` on that host for the owner member,
   handing over a **sealed bundle**: the fleet card (for each host: name,
   cid, host static, routes, relay) encrypted with a key `k` that appears
   only in the QR. The pairing host stores the bundle but cannot read it.
3. The QR carries `{v:4, cid, name, routes, psk, k, exp}`: the existing
   `QRBlob` (`pkg/remote/wire/v3.go:331-341`) plus `routes` and `k`. It
   stays well within a scannable QR.
4. The phone tries the routes in parallel, runs IKpsk2 with the pairing
   host (built), and gets `PairWelcome` with its device id, relay token and
   the bundle. It opens the bundle with `k` and pins every host static.
5. The pairing host admits the phone as a device of the owner and emits
   `device.paired`. The desktop's link agent then calls `AddMyDevice` (the
   phone's static, name and a person-key signature) on **every other host**
   over its existing channels. A host that is offline gets it when it comes
   back; the phone shows it as "waiting for home to come online".
6. The phone connects to each host on its own best path. One pairing
   covers the fleet; revocation stays per device per host, and "Remove this
   phone" on the desktop fans out to every host.

Phone screen right after:

```
┌──────────────────────────────┐
│ Hosts                        │
│ ● laptop   relay vps-1  62ms │
│ ● vps-1    tailnet      21ms │
│ ◌ home     waiting…          │
│                              │
│ Now                          │
│ ⟳ api · fix flaky test       │
│   reviewer · on vps-1  3m    │
│ ? web · deploy plan          │
│   needs you · on laptop      │
└──────────────────────────────┘
```

Push: the phone registers with each host (`push.register`, built), each host
seals its own pushes (RFC 8291) and hands them to its relay; the relay never
sees content. Native push through a self-hosted relay is an open question
(§6.2).

### (c) Invite a teammate to a host or a project

**Budget: under 60 seconds for the teammate**, from opening the link to
seeing the threads; one click for the owner to approve.

**Owner** (desktop: Host › Members › Invite; Project › Share; TUI and CLI:
`deck invite`):

```
$ deck invite --host vps-1 --project api --role member --expires 24h
Invite for ana? (name shown to you only; she picks her own)  ana
Role        member on project "api" (vps-1)
Spends      your plans on vps-1: claude (Max), codex (Plus)
Link        https://relay.dami.dev/join#p=eyJ2Ijo0…     (one use, 24 h)
            deck://join?v=4&p=eyJ2Ijo0…
```

```
┌ Invite to vps-1 ─────────────────────────────────────────────────────┐
│ Scope   ○ whole host   ● project  api ▾                              │
│ Role    ○ viewer   ● member   ○ admin                                │
│ Expires 24 h ▾                                                       │
│                                                                      │
│ ⚠ Agents ana starts run on vps-1 under YOUR sign-ins.                │
│   Her messages spend your claude and codex plans.                    │
│   Her turns run in ask mode at most.                                 │
│                                       [ Cancel ]  [ Create link ]    │
└──────────────────────────────────────────────────────────────────────┘
```

An invite is per host. An invite to several hosts is a bundle of per-host
invites in one link; the teammate's client joins each.

**Teammate** opens the link (desktop, TUI `deck join <link>`, or phone):

```
┌ Join dami's vps-1 ───────────────────────────────────────────────────┐
│ dami invited you as member on project api.                           │
│ Host fingerprint  SHA256:7Qx2…kP                                      │
│                                                                      │
│ Agents run on dami's machine with dami's sign-ins.                   │
│ Your turns run in ask mode. Destructive approvals need an admin.     │
│                                                                      │
│ Your name  ana                Device  ana's ThinkPad (desktop)       │
│                                       [ Decline ]  [ Join ]          │
└──────────────────────────────────────────────────────────────────────┘
```

1. The client creates her person key if she has none (first Deck install).
2. It dials the routes in the link: tailnet if she shares the tailnet,
   direct, then the relay. IKpsk2 with the invite's slot. The join request
   carries her device static, person key, display name and a signature.
3. The host holds her as **pending** and asks every online admin:

```
┌ Join request · vps-1 ────────────────────────────┐
│ ana  (person SHA256:Lp0c…2Q)                     │
│ wants to join as member on project api           │
│ device: ana's ThinkPad (desktop) · via relay     │
│                         [ Deny ]  [ Approve ]    │
└──────────────────────────────────────────────────┘
```

   Approval is explicit even with a valid code, like Syncthing's "accept
   device" prompt (https://docs.syncthing.net/intro/getting-started). On
   approve, her device is pinned and the grant written.

4. **What she sees.** A group "dami's vps-1" in her sidebar with only the
   granted projects and their threads. It is the full room through the same
   `deck.v2` calls the owner uses: timeline with tool cards, changes, tasks,
   the agents pane, worktrees. Terminals and Sign in are admin-only. Every
   thread row carries "on dami's vps-1". The composer footer says
   `Runs on dami's vps-1 · ask mode · dami's claude plan`. Her messages enter
   the thread's normal queue as `author: ana`; `@` and `$` work as for the
   owner. Rate limits per member reuse the existing brakes
   (`session/brakes.go`).
5. **Her other devices.** From her desktop she links her phone exactly as
   in (b): her link agent fans out `AddMyDevice` to every host where she is
   a member. The owner is not asked again; the host checks her person-key
   signature and that the caller is already one of her devices.
6. **Tailscale is a faster path, not a gate.** Same organisation tailnet:
   the admin adds `tag:deck` hosts on port 7743 to a grant for her group.
   Different tailnets: Tailscale node sharing works, with caveats (the
   recipient must be an admin of her own tailnet, sharing is beta, the
   shared node is quarantined and cannot dial out; https://tailscale.com/docs/features/sharing).
   Deck's channel is opened by her side, so a quarantined host still works.
   No Tailscale: the relay. Nothing else is required.

**Presence.** Each client sends `Presence{thread, state: viewing|typing|idle}`
on the thread subscription. It is ephemeral, never stored, and expires after
30 s without a heartbeat. The room header shows avatars of who is looking;
"ana is typing" sits above the composer; the fleet rail shows a dot per
online member per host. This replaces `peer.status` polling.

**Approvals per role** follow §4: an approval belongs to the thread and is
shown to every present client whose role can answer it; the first valid
answer wins (`approval.resolved`); an answer by anyone but the owner writes
an audit line and shows "approved by ana" in the timeline.

**Removing her.** Host › Members › ana › Remove: the grant goes, her devices
are revoked, live channels drop within seconds (the `devstore` watcher), and
if Deck manages the Tailscale side through the OAuth client it removes the
grant or share in the same click.

### (d) The fleet view

**Budget:** the rail shows every reachable host within 1 s of opening the
app (cached view at once, live within one round trip); an approval on any
host reaches the Inbox within 1 s of the engine raising it.

Each client holds one `WatchHost` summary stream per host and merges them
keyed by `(host cid, thread id)` (chapter 07). No message bodies travel
until a thread is opened.

Desktop (the rail on the left of Inbox + Now; chapter 04 owns the look):

```
┌ Hosts ─────────────────────────┐┌ Now ──────────────────────────────────────────┐
│ ● laptop   local               ││ Needs you                                     │
│   claude ✓ codex ✓             ││  ? deploy plan        web · lead      on laptop│
│   2 running                    ││  ! rm -rf build/      api · reviewer   on vps-1│
│ ● vps-1    tailnet · direct 18 ││ Running                                       │
│   claude ✓ codex ✗ Sign in     ││  ⟳ fix flaky test     api · lead  12m  on vps-1│
│   3 running · ana viewing      ││    └ ⟳ reviewer       checking diff    on vps-1│
│ ◌ home     offline 3m          ││  ⟳ docs pass          web · writer 2m on laptop│
│   last: 1 running              ││ Review                                        │
│ + Add a host                   ││  ✓ migrate config     api · lead      on home  │
└────────────────────────────────┘└───────────────────────────────────────────────┘
```

- **Per host:** name, owner (for teammates: "dami's vps-1"), route badge,
  latency, Deck version (an "Update" chip when it differs from the
  client's), CLI sign-in state per kind, count running, members present.
- **Under each host** (expanded): its agents with state (working, waiting
  for approval, idle), context use, current thread and worktree.
- **Every thread and agent** everywhere carries `on <host>`; agents are
  qualified when two hosts have the same profile (`vps-1/reviewer`).
- **Fleet-wide Inbox:** Needs you merges approvals and questions from every
  host; "jump to next needing approval" (chapter 04) walks across hosts.
- **Offline host:** greyed, with "last seen", what was running, queued
  sends, and "Wake via SSH" (`systemctl --user restart deck` over SSH)
  when the host has an SSH alias.

TUI: one key opens the same view (chapter 04 owns keys):

```
 Hosts                            Now
 ● laptop  local        2 ⟳       ? deploy plan        web       laptop
 ● vps-1   tailnet 18ms 3 ⟳ ana   ! rm -rf build/      api       vps-1
 ◌ home    offline 3m             ⟳ fix flaky test     api  12m  vps-1
                                   ⟳ docs pass          web   2m  laptop
 a add host  i invite  s sign in  enter open  m move  ? help
```

CLI for scripts: `deck host ls` (name, route, version, CLIs, running) and
`deck host status vps-1`.

### (e) "Runs on" when starting a thread, and moving a thread

**Budget:** picking a host adds no step (it defaults); a move of a thread
with a clean or small dirty tree finishes in under 30 s on a normal link,
plus the agent's own start time.

**Starting.** The composer of a new thread shows `Runs on: vps-1 ▾`. The
default is the project's home host: an always-on host that has the project,
else this machine. The picker shows, per host, whether the project is
checked out and whether the needed CLI is signed in:

```
┌ Runs on ─────────────────────────────────────────┐
│ ● vps-1   api checked out · claude ✓              │
│ ○ laptop  api checked out · claude ✓              │
│ ○ home    no checkout  → Clone on home            │
│           codex ✗ not signed in                   │
└───────────────────────────────────────────────────┘
```

"Clone on home" runs `git clone` on that host from the project's remote,
with the host's own git credentials.

**Moving** ("Move to host…" on a thread, or drag it onto a host in the
rail; admin only). D17:

1. **Preflight on the target:** the repo is present (or offer Clone), the
   CLI is installed and signed in, enough disk, the source worktree is clean
   or will be bundled.
2. **Freeze** the source thread at a turn boundary, or interrupt with the
   user's consent.
3. **Export** from the source: the thread log, tasks and memory notes, plus
   a git bundle of the branch and a patch of uncommitted changes (the
   capsule code from `sub`, re-hosted on Deck Link). The admin's link agent
   streams it to the target's `ImportThread`.
4. **Resume** on the target: agents start fresh from a hand-off summary plus
   recent turns. CLI session files are never copied (they are per machine
   and can hold credentials).
5. **Tombstone:** the source thread becomes read-only, "Moved to vps-1 at
   14:02 →", and the target links back. "Continue on laptop" is the same
   flow without the tombstone (a fork).

```
┌ Move "fix flaky test" to vps-1 ──────────────────────────────────────┐
│ ✓ api checked out on vps-1                                           │
│ ✓ claude signed in on vps-1                                          │
│ ✓ 2 uncommitted files will be carried as a patch                     │
│ ! Agents restart on vps-1 from a summary; their CLI sessions do not  │
│   move.                                                              │
│                                          [ Cancel ]  [ Move ]        │
└──────────────────────────────────────────────────────────────────────┘
```

**"Keep running on vps-1 when the lid closes"** is this move, offered when
the laptop is about to sleep (or the GPUI app is quitting) with an agent
mid-task and an always-on host has the project. One click; the phone then
follows the thread on vps-1.

### (f) Offline and reconnect

**Budget:** the cached view renders at once with no spinner; the reconnect
after the network returns completes within one backoff step (≤ 30 s, usually
1–2 s) and catches up from the last revision without refetching threads.

- **The host is authoritative.** Clients cache the last host summary and
  the last pages of open threads: the link agent on disk under
  `~/.deck/link/cache/` (0700), the phone in its existing sealed relay
  snapshot plus local storage. The cached view is greyed with a badge.
- **Badge:** `offline 3m` on the host row and in the header of each of its
  threads, from the last contact (or the relay's `last_seen`).
- **Queued sends.** The queueable set is the one in `docs/mobile-contract.md`
  §6 (send, new thread, continue, answer an approval bound to its id and
  digest, cancel a queued item); interrupts, cancel and mode changes are
  never queued. Each queued item shows in the composer area as "queued ·
  sends when vps-1 is back · expires in 8 h". Where they wait:
  - the host has a relay: the item goes, sealed (Noise K), to the host's
    relay mailbox (built), so it is delivered even if your laptop sleeps;
  - no relay: it waits in the link agent's outbox and goes on reconnect.
  Delivery is at least once, effect exactly once (`client_op_id` and the
  queue's `qid`). Receipts come back as "sent", or "dropped: approval
  already answered".
- **Laptop asleep:** its threads show "laptop asleep"; threads on VPSs keep
  running, and phones still get push from those hosts.
- **Host restart:** systemd restarts it (`Restart=on-failure`); drain on
  SIGTERM is kept; clients resume by revision.

```
┌ api · fix flaky test ─────────────── on vps-1 · offline 3m ┐
│ (greyed timeline, last revision 1842)                      │
│ …                                                          │
│ ┌ queued · sends when vps-1 is back · expires in 8 h ─────┐ │
│ │ also run the race tests                    [ Cancel ]   │ │
│ └─────────────────────────────────────────────────────────┘ │
│ [ Message (will queue) …                              ]    │
└────────────────────────────────────────────────────────────┘
```

### (g) CLI sign-ins per host: the Sign in terminal

**Budget: under 60 seconds**, dominated by the CLI's own device-code login.

- Each host reports, per CLI: found, version, and
  `signed_in: yes | no | unknown`, from the CLI's own status command (exit
  code only) or from the result of the first run. Deck never opens
  credential files (`~/.claude`, `~/.codex` …).
- The host card shows `codex ✗ not signed in · Sign in`. A thread whose
  picked CLI is signed out on its host says so in the Runs on picker and in
  the composer.
- **Sign in** opens a terminal pane on that host (the existing
  `TerminalOpen` RPC, `proto/deck/v1/deck.proto:68`) running the CLI's own
  login command, usually a device-code flow that works headless. Deck relays
  the terminal and never reads the result; it re-runs the status check when
  the terminal exits.
- CLI: `deck host signin codex` on the host runs the same command in your
  terminal.
- Admin only (a terminal on the host is the host user's full power).
  Teammates see sign-in state but cannot start a sign-in.
- API keys for CLIs are set on the host by its owner (environment, the
  unit's `Environment=`); Deck never syncs them.

```
┌ Sign in · codex on vps-1 ────────────────────────────────────────────┐
│ $ codex login --device-auth                                          │
│ Open https://… and enter code  WXYZ-1234                             │
│ Waiting for approval…                                                │
│ ✓ Signed in.                                                         │
│                                   codex ✓ signed in on vps-1  [Done] │
└──────────────────────────────────────────────────────────────────────┘
```

(The exact login command per CLI lives in its adapter and changes with the
CLI; the one above is illustrative.)

---

## 4. Roles and approvals

Roles are per host or per project (D15). Yolo stays a per-device grant
(`docs/remote-contract.md` R15) and only an admin can give it.

| Action | viewer | member | admin |
|---|---|---|---|
| See granted threads, changes, tasks, agents, presence, plan windows | ✓ | ✓ | ✓ |
| Send messages, steer, cancel own queued items | | ✓ | ✓ |
| Start threads in granted projects | | ✓ | ✓ |
| Delegate to a profile, add an agent to a thread (thread scope only) | | ✓ | ✓ |
| Answer **normal** approvals and questions | | ✓ | ✓ |
| Answer **destructive** approvals (`pkg/remote/policy` tier) | | | ✓ + passkey |
| Set a thread's mode above ask; turn on yolo | | | ✓ + passkey |
| Destructive actions: move, delete or archive a thread, revert, discard a worktree | | | ✓ (+ passkey from a remote device) |
| Terminals, Sign in, host network settings | | | ✓ |
| Change defaults, profiles and host settings (Settings only, D5) | | | ✓ |
| Invite, change roles, remove members and devices | | | ✓ |
| Remove the owner | | | never |

Rules behind the table:

- **Members are capped at ask mode.** A turn started by a member's message
  runs in the thread's mode capped at `ask`, whatever the thread is set to.
  This generalises today's "remote-originated turns are capped at ask".
  A project setting, "teammates' turns need an admin to approve edits",
  covers stricter teams.
- **Passkeys for destructive actions.** From a phone: a fresh WebAuthn
  assertion over the action digest (built, `docs/mobile-contract.md` §7).
  From a remote desktop: the platform passkey (Touch ID, Windows Hello, a
  security key) through the GPUI app. From the local socket (the person at
  the host's own keyboard): no assertion, as today's desktop transport
  (`docs/remote-contract.md` §10). A remote TUI shows destructive approvals
  as "answer on your phone or desktop"; v3 adds no passkeys to the TUI.
- **Teammates spend the host owner's plans.** Agents run under the host's
  CLI sign-ins. The invite, the join screen and the composer footer say so;
  members see the plan windows (this replaces `share_limits`).
- **The role checked is the lowest that applies:** project grant over host
  grant where both exist, and a tailnet app capability can only lower it.
- **Every answer is attributed.** The timeline shows who approved; the audit
  log records it.

---

## 5. What it replaces, and the migration

The deletions themselves (files, lines, order, tests) are in chapter 02.
This section says what Deck Link takes over and how users move.

### 5.1 What goes

| Today | Problem (evidence) | Replaced by |
|---|---|---|
| **Peer link**: embedded SSH server on :7722, Deck-as-relay, `deck peer pair/add/list/ping/remove/relay/fingerprint` | Pairing needs an open inbound port or a third Deck with a public port; relaying needs three opt-ins on three Decks (`docs/peers-design.md` §1, `f3bcdc2`); a teammate is a machine key; shares keyed by name (`session/share.go:115-130`); shared rooms are a text mirror (`SharedMirror`, `deck.proto:1268`); peer state files rewritten by several processes with no cross-process lock (`peer/outbox.go:50-60`) | Hosts, members, roles; the five paths; `deck invite` |
| **`deck remote` v2** (HTTPS + WebSocket, per-PC PWA) | A second phone protocol and device store | The phone on Deck Link (D19) |
| **Two extra pairing flows** (peer argon2 code + `deck://pair`; v2 HTTP code) | Three pairing systems, two relays, five ways to connect, each with its own words and store (research-team §2.2) | One: the IKpsk2 slot, armed for an invite or a device |
| **Desktop "Pair with a Deck"** | It is the v2 phone protocol and gets only "the open session's messages" (`desktop-shell/README.md:22`); the Team page points to a Settings page that has no peer controls (`desktop-shell/src/renderer/ui/Team.tsx:409`), so a desktop-only user can never add a teammate | Add a host, and joining by invite link |
| **`deck mobile setup/pair/daemon/projects/connect`, `/remote`, `/link`** | LAN mode needs Node and wrangler supervised by Deck (`docs/mobile-lan.md` §2); hosted mode needs your own Cloudflare deploy (`docs/mobile-deploy.md`) | `deck host`, `deck relay`, Link a phone |
| **`deck serve`** | Refuses to run unless peers are on (`internal/cmd/serve.go:201-208`); takes the thread lock exclusively | `deck host run` (chapter 07's daemon) |

### 5.2 Commands, old to new

| Old | New |
|---|---|
| `deck serve`, `deck mobile daemon`, `deck mobile host` | `deck host run` (`deck serve` stays one release as an alias that prints a notice) |
| systemd example `deck-serve.service` | `deck host up` writes `deck.service` |
| `deck mobile install` | `deck host up` (also on macOS/Windows via `autostart`) |
| `deck mobile pair`, `deck remote pair`, `deck peer pair`, `/link` | `deck host link` (your own device), `deck invite` (a teammate) |
| `deck mobile devices/revoke`, `deck remote devices` | `deck members`, `deck members rm`, `deck devices rm` |
| `deck mobile projects add/rm` | `deck host projects add/rm` |
| `deck mobile setup --relay` | `deck host relay <url>`; `deck relay` to run one |
| `deck peer add/ping/list` | Add a host; `deck host ls`, `deck host status` |
| — | `deck link --stdio` (SSH carrier), `deck link agent` (client side), `deck join <link>` |

### 5.3 Data

| Data | Action |
|---|---|
| v3 `devices.json` phones | Become devices of the owner member; keep working with no re-pair |
| v3 relay settings in `mobile.json` | Become the host's relay setting |
| `~/.deck/peers/` | Renamed to `peers.retired-<date>/`, one notice: "Team connect was removed; invite teammates with `deck invite`". Pins are not migrated (they are Deck keys, not people) |
| `store.Thread.Shares` | Dropped (names cannot be bound to people); the notice lists the threads that had shares so the owner can invite those people |
| Desktop `servers.json` (v2 pairings) | Shown once: "Add this Deck as a host" with the address pre-filled |
| v2 phone devices | Re-pair with Link a phone |

### 5.4 Sequencing

Chapter 00 says roles and teammate access ship in the **same release** that
deletes the peer link, so teammates are never left with nothing. `deck
remote` v2 goes once the GPUI app and the phone are on Deck Link.

---

## 6. Build order and open questions

### 6.1 Build order

Each step ships on its own and leaves Deck working. Steps L1 onward assume
chapter 07's one-daemon-per-host foundation.

| Step | What ships | Users can now… |
|---|---|---|
| **L0** | Move the peer helpers `pkg/remote` imports (`NewPairCode`, `Lockout`, `WritePrivateFile`, …) out of `internal/deck/peer` (chapter 02, step 0). No behaviour change. | (nothing visible) |
| **L1** | `deck host run` serves every project; local socket; `members.json` with the owner; existing phones become owner devices; per-call role checks (owner only). | run one daemon per machine; TUI and desktop attach to it |
| **L2** | Link agent; SSH stdio path; `deck host up`; desktop Add a host (push binary, unit, SSH pinning); CLI sign-in state and Sign in terminal; fleet rail with route badges. | put agents on their own VPS and use them from desktop and TUI over SSH |
| **L3** | System Tailscale path and direct path; the path race and background upgrade; latency; `WhoIs` extra check. | always-on, fast reach across their machines |
| **L4** | `deck relay` (Go, `relay/v1`, mailbox, snapshots, VAPID push, TLS via ACME or behind a proxy); lanes for any device; QR v4 with routes and sealed fleet card; `AddMyDevice` fan-out; the phone on Deck Link. | link a phone once and see every host, with no Cloudflare account or Node |
| **L5** | Person keys; invites; join approval; viewer/member/admin; ask cap; destructive approvals by admin with passkey; presence; author on every message; audit. **Delete the peer link and team UI in this release** (chapter 02). | invite teammates with direct access |
| **L6** | Embedded tsnet (interactive login, then OAuth-minted one-off keys); Headscale `ControlURL`; `deck-slim`; size gate in CI. | join a tailnet with no system Tailscale; run fully self-hosted |
| **L7** | Move a thread; Continue on…; "Keep running on vps-1 when the lid closes"; Clone on host. | move work between machines |
| **L8** | Delete `deck remote` v2, `internal/mobilecontrol`, the `deck mobile` and `deck serve` aliases. | (cleanup) |

### 6.2 Open questions (each with our current pick)

1. **Tailscale device limits.** Embedded tsnet adds one tailnet device per
   host on top of the machine's own node if it also runs Tailscale. Pick:
   tsnet is only used when system Tailscale is absent, so a host is never
   two devices. Check the current plan limits before L6 and show the count
   in the network step if the user is close.
2. **Headscale gaps.** Peer relays and app capabilities need checking per
   Headscale version (v0.29.x at the time of writing, *secondary*). Pick:
   Deck needs neither; the role cap from app capabilities is simply absent
   on Headscale. Document the tested version in the release notes.
3. **Phone app size and tsnet.** Pick: no tsnet in the phone in v3 (it would
   add roughly 14–23 MB, *secondary*, and iOS background limits make an
   in-app tailnet unreliable). The phone uses the Tailscale app when
   installed, else direct or relay. Measure on the Expo build before
   reopening.
4. **Push on a self-hosted relay.** Web push works fully self-hosted today:
   the relay holds a VAPID key and posts sealed payloads
   (`docs/mobile-contract.md` §8). Native push to the store app needs the
   app publisher's APNs and FCM credentials, which a self-hosted relay does
   not have. Pick: run a small hosted **push gateway** that accepts only
   sealed, content-free payloads from any relay and forwards them to APNs
   and FCM; self-hosters who refuse every hosted piece get foreground-only
   updates plus web push. This needs a decision on who runs the gateway
   before L4 ships push.
5. **Lost person key.** If the only device holding a person key is lost,
   that person cannot add devices or accept invites. Pick: desktops hold it
   by default and phones only on request; a host admin can re-grant a
   person under a new key, and the old member id is revoked.
6. **Direct host-to-host transfers.** Moves stream through the admin's link
   agent. For large trees between two VPSs that is slow. Pick: keep it
   client-driven in v3; revisit a scoped host-to-host grant after L7 if
   moves of more than a few hundred MB are common.
7. **Windows hosts.** `deck host up` uses `autostart` (Scheduled Task) on
   Windows; the SSH stdio path needs OpenSSH on the client (built into
   Windows 10 1809+). Pick: support Windows as a host from L2, test Linux
   first.

---

## 7. Sources

Repo (verified at `eec5677`): `pkg/remote/identity/identity.go`,
`pkg/remote/devstore/devstore.go:63-78`, `pkg/remote/wire/v3.go:331-341`,
`pkg/remote/daemon/pair.go`, `pkg/remote/web/src/state3.ts:119`,
`internal/deck/session/share.go:115-130`, `internal/deck/peer/outbox.go:50-60`,
`internal/cmd/serve.go:201-208`, `desktop-shell/src/renderer/ui/Team.tsx:409`,
`proto/deck/v1/deck.proto:68,87-104`, `install.sh:87-96`,
`docs/mobile-contract.md`, `docs/remote-contract.md`, `docs/mobile-lan.md`,
`docs/mobile-deploy.md`, `docs/examples/deck-serve.service`.

External (from the connection research; *secondary* = third-party write-up
or search snippet only, check before quoting):

- tsnet: https://pkg.go.dev/tailscale.com/tsnet, https://tailscale.com/blog/tsnet-virtual-private-services
- Tailscale connection types and DERP: https://tailscale.com/docs/reference/connection-types, https://tailscale.com/docs/reference/derp-servers
- Grants and app capabilities: https://tailscale.com/docs/features/access-control/grants/grants-app-capabilities, https://tailscale.com/blog/grants-ga
- Auth keys: https://tailscale.com/kb/1085/auth-keys
- Sharing: https://tailscale.com/docs/features/sharing, https://tailscale.com/docs/reference/inviting-vs-sharing.md
- Tailscale SSH: https://tailscale.com/kb/1193/tailscale-ssh; Serve and Funnel: https://tailscale.com/kb/1223/tailscale-serve
- Binary size: https://tailscale.com/kb/1207/small-tailscale/, https://pkg.go.dev/tailscale.com/cmd/omitsize, https://pub.dev/documentation/tsnet_flutter/0.3.0 (*secondary* for the mobile figures)
- Headscale: https://pkg.go.dev/github.com/juanfont/headscale, https://www.infralovers.com/blog/2026-05-22-headscale-self-hosted-tailscale-alternative/ (*secondary*)
- Coder (embedded tailnet at scale): https://coder.com/docs/ai-coder/agents/architecture
- Add-a-host over SSH: https://zed.dev/docs/remote-development, https://www.jetbrains.com/help/idea/remote-development-a.html, https://devpod.sh/docs/how-it-works/overview, https://devpod.sh/docs/managing-providers/add-provider
- Codex app hosts and Codex Remote: https://codex.danielvaughan.com/2026/05/21/codex-remote-connections-mobile-pairing-ssh-hosts-enterprise-access-tokens/, https://codex.danielvaughan.com/2026/07/10/codex-remote-ga-qr-relay-architecture-mobile-approvals-ssh-host-cli-developer-guide/ (*secondary*)
- Claude Code Remote Control and self-hosted environments: https://code.claude.com/docs/en/remote-control, https://code.claude.com/docs/en/self-hosted-environments
- Syncthing device trust: https://docs.syncthing.net/intro/getting-started, https://manpages.ubuntu.com/manpages/mantic/man7/syncthing-security.7.html
- Happy (QR + self-hostable E2E relay): https://github.com/hitosea/happy
- Grok Bot shared computer: https://www.unite.ai/xai-launches-grok-bot-always-on-ai-teammates-with-their-own-cloud-computers/ (*secondary*; primary https://x.ai/news/introducing-grok-bot not fetched)
- iroh (reference design for a key-addressed relay): https://docs.rs/crate/iroh/latest
