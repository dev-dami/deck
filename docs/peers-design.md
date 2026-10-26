# Deck peers: team link between Deck instances

Status: implemented on branch feat/agent-pools. In `internal/deck/peer`: the transport, pairing, pinning, supervisor and `deck peer` commands; `peer.status` with presence; `console.post` and `console.read` team chat with idempotency keys and the outbox; capsule transfer (`sub.closure`/`sub.objects`); code contracts and shared threads (`docs/multiplayer-design.md` §8.4, §8.5); ops contracts (§3) run in the `internal/deck/vsh` sandbox (§4); relaying through a paired Deck (§1); and `deck serve` (§8 step 7). Not built: the full-screen team console (§5; the Team, Chat and Shared panes cover its parts) and the per-peer mirror store of §6 (shared threads are read live instead). The outbox is one file, `~/.deck/peers/outbox.json`, not one per peer.

Decisions:
- Peer-to-peer mesh between Deck instances.
- **Deck's own app-internal SSH** (golang.org/x/crypto/ssh) is only an encrypted, authenticated pipe. It is not a login service. It accepts one thing: **remote MCP calls to exactly the 2 tools, `search` and `execute`**.
- Wire protocol is **MCP's JSON-RPC 2.0** (`sourcegraph/jsonrpc2`, already a dependency). No gRPC.
- **Word-based grants are task contracts**: a person tells their agents what to do and what result to bring back. Agents can only send back **contracted results**.
- Remote-originated work runs in a **virtual bash instance** (in-process, allow-list only), never in the host shell.
- v1: remote ops, contracts, team console, thread sync.

## 1. Transport: app-based SSH, MCP only

```
PC Deck                                              VPS Deck
 ssh client (Deck identity) --TCP :7722--> embedded ssh server (Deck host key)
   one channel type: "mcp"  ->  MCP JSON-RPC: initialize, tools/list, tools/call
   tools/list returns exactly: search, execute
```

- **Nothing a person can use.** The server rejects every SSH request except opening an `mcp` channel: no `shell`, `exec`, `pty-req`, `subsystem`, `direct-tcpip`, `tcpip-forward`, `env`, agent or X11 forwarding. Auth is public-key only, and the key is a *Deck instance* key (pinned peer), not a user account. There are no passwords, no `authorized_keys`, no `~/.ssh` and no host sshd involved.
- **Two-way:** SSH lets the accepting side open channels back to the dialer, so after one outbound connection each Deck can call the other's two tools. A PC behind NAT only needs to dial out.
- **Relaying:** a Deck with `peers.relay` on passes links between two of its pinned peers. The origin opens a `relay@deck` channel that names the target by key fingerprint. The relay accepts it only when both peers are pinned there and the target has a direct link to it. It then opens `relayed@deck` on the target and copies bytes between the two channels (16 relays in total and 4 per key, a 90 s idle timeout). The target accepts `relayed@deck` only from a peer whose row has `relay: true`, and only for a key pinned there. It serves the link through its normal server path: the same handshake limits and lockout, its own connection caps, and login only with the key the relay named. The inner link is a normal peer link: mcp channels only, and the normal allow list. `peers.remote[].via` makes the supervisor dial through a relay when the row has no address or the direct dial fails. The relay types are refused on every other link and on relayed links. Pairing goes the same way for two Decks that are paired with the relay but not with each other: the dialer opens `relay-pair@deck` naming the fingerprint from the invite, the relay checks the same rules and opens `relayed-pair@deck` on the target, and the target takes it only while it waits for a code (waiting is the consent; no `relay` row is needed). The connection may log in only as the pairing user, only with the key the relay named, and gets the normal code checks and attempt limit, so the relay cannot pair without the code. The dialer tries this only after the invite's address gives no answer, through each peer it has a direct link to. A refusal goes back with the target's reason, so each side can say which Deck to fix. After the pin, the dialer saves `via` for the target and the waiting Deck sets `relay: true` on the relay's row.
- **Own identity and pinning:** each Deck makes its own ed25519 identity in `~/.deck/peers/` (restricted ACL). Pairing (Settings › Team › Add a teammate, or `deck peer pair` / `deck peer add`) exchanges a short one-time code, shows both fingerprints for confirmation, and pins the other side's key. Only pinned keys connect. Host keys are pinned, never trust-on-first-use.
- **Pairing runs on the same listener.** While Deck's listener runs, a pairing code is armed on it: same port as the link (7722), nothing extra to open. While a code is armed, and only then, the server also lets in the SSH user `deck-pair` with any ed25519 key; that connection gets the `deck-pair` channel and nothing else (no `mcp`, no other channel, no global request but keepalive), runs the code exchange (argon2id + HMAC bound to the session id and both keys; single use, 3 wrong codes burn it, 5-minute expiry, per-IP and total connection caps), and never touches the pinned store. With no code armed `deck-pair` is refused like an unpinned key and counts toward the lockout; pre-auth limits and lockout apply to pairing logins as to any other, and a pairing login clears no lockout. Pinned peers keep connecting as before while a code is armed. Only the local person arms a code (one at a time; a new one replaces the old), and it disarms on success, burn, expiry, cancel or when the listener stops; moving the listener (e.g. to all interfaces) keeps it armed. Without a running listener, pairing opens a listener of its own on `peers.listen.addr` (else all interfaces :7722) and reports a busy port instead of picking another. The waiting side says when its address is loopback only (the other Deck cannot reach it) and offers one key to listen on all interfaces.
- **Invite.** The waiting side copies one line, `deck://pair?v=1&host=…&port=…&code=…&fp=SHA256:…&name=…&exp=…` (host from `$SSH_CONNECTION`, else the interfaces, else the host name; `alt=` for other addresses), which the other Deck pastes to fill address, code and expected fingerprint. A host key other than the invite's fingerprint ends pairing before the code is used. The invite holds the code: it is never logged or put in a snapshot or error.
- **Hardening:** `listen` is off by default; optional bind address (e.g. WireGuard); connection and handshake limits; per-IP auth-failure lockout; idle timeouts; request size caps; no plaintext fallback; fuzz tests on the handshake and channel-open path.
- **Windows and Linux:** pure Go, identical behavior. No `ssh.exe`.
- **Retries and persistence:** supervisor per peer with exponential backoff + jitter (1 s to 60 s), SSH keepalive every 15 s (3 misses = dead), re-`initialize` after every reconnect. States: `idle -> dialing -> connected -> degraded -> backoff`. An on-disk **outbox** (`~/.deck/peers/<peer>/outbox.jsonl`) holds contracts, results and console posts with idempotency keys until acknowledged.

## 2. What the 2 tools expose to a peer

Peers see the same two tools; what `search` finds and `execute` runs is a **peer-scoped catalog** (a new `hub.Register` with a per-caller allow-list), filtered by the receiving Deck's `allow_inbound` for that peer:

| Op | What | Default |
|---|---|---|
| `peer.status` | my agents, state, context %, uptime | allowed |
| `console.read {since}` / `console.post {text}` | team feed | allowed (posts are plain status text) |
| `contract.propose {...}` | offer a contract to run here | needs role `contractor` |
| `contract.status {id}` / `contract.result {id, result}` | progress and the contracted result | allowed for that contract only |
| `history.read {thread, cursor, limit}` | paged thread mirror (read-only) | allowed |

Nothing else is reachable remotely: no git, no gh, no `thread.send`, no raw commands. Everything returned from a peer is re-redacted and size-capped locally. Long operations return `queued` and report via a result; hub timeouts (55 s / 60 s) never block the link.

Agents on the local Deck reach a peer with the same tools: `execute {op: "peer.call", peer, op, args}` forwards one of the peer-scoped ops above (`vps` / `pc` names). Qualified agent names like `vps/claude` appear only in the console, never as shell input.

## 3. Contracts: word-based grants for working together

As built: the person asks in the Team pane (`o` on a teammate). The parser knows the presets `podman-read` and `systemd-read`, and makes every field a string of at most 200 characters. The requester confirms the card; the contractor runs a contract at once when its scope is inside `max_scope`, at most 2 at a time per teammate. Only Claude can run one (`agent.OpsSafe`: `--tools ""` and its built-in tools disallowed); the other CLIs fail closed. The audit log is `~/.deck/peers/audit.jsonl`, not the console log; it keeps the last 5000 lines.

A contract is an agreement between a person and agents: what to do, what they may touch, and what comes back.

```
Contract {
  id, title, requester (person + peer), assignee (agent kind/name on target)
  goal        text the agent receives as its task
  scope       commands: allow-listed service shims (podman-read, systemd-read, ...)
              reads:    scratch inputs the contract provides
              ttl, limits (time, output bytes, calls)
  result      JSON Schema: the ONLY thing allowed to leave (fields, types, maxLength)
  status      proposed -> confirmed -> running -> delivered | failed | expired | revoked
}
```

- **In words:** the person types in the console, for example `ask vps to report podman containers: name, state, uptime, restarts`. A deterministic template parser builds the contract (scope from presets such as `podman-read`, result schema from the listed fields, default TTL) and shows a **confirmation card**; nothing runs until the person confirms. An LLM-assisted parse is optional and goes through the same card. Text produced by agents or tools can never create, widen or confirm a contract.
- **Scoped to the owner:** the receiving Deck's owner sets an outer limit once in config (`max_scope`, e.g. only `podman-read`). Contracts from peers can only choose inside it. Out-of-scope requests are rejected, not escalated, so a headless Deck needs no live approver. To extend, the person amends the contract in words (`also include image tags`) and confirms again.
- **Results only:** the agent delivers by calling `contract.result` with JSON that must validate against the contract schema (types, no extra properties, per-field maxLength, total size cap, redaction pass). Invalid results go back to the agent to fix (limited retries); nothing else crosses the link. The agent's free-form chat and tool output stay on the executing Deck, visible to its owner, and are never sent to the requester.
- **Audit:** every contract, command run, denial and result is written to the console log.

## 4. Virtual bash instance (sandbox)

As built (`internal/deck/vsh`):
- It parses each script with mvdan.cc/sh and refuses the whole script, before anything runs, when it uses loops, case, functions, aliases, variables, substitutions, arithmetic, here-docs, background jobs or unquoted braces.
- A command name must be a plain word that names an allowed builtin (echo, printf, true, false, :, test, [, cat, head, tail, grep, sort, uniq, wc, cut, tr, jq) or a configured shim, so `env`, `command`, `\podman` and `/usr/bin/podman` are refused.
- Every refusal gives exit 126 and one stderr line that starts with `vsh:` and says why; a timeout gives exit 124.
- Redirections, globs, `test` and the builtins see only the scratch directory through an `os.Root`, so absolute paths, `..` and symlinks that lead out are refused, and writes count against the scratch quota. jq reads only scratch files and sees an empty environment.
- A shim runs its fixed binary with os/exec, no shell, the environment `PATH=/usr/bin:/bin LANG=C`, no stdin and the scratch directory as cwd, in a process group that is killed at the timeout.
- Shim argv must match an allowed pattern word by word: literals, `{target}`, `{n}` (0 to 100000), `{format}`, `{time}` and optional groups in brackets.
- Package runners, fetchers, shells, interpreters, sudo, su and runner wrappers such as env and xargs are never grantable as shims, and neither are destructive podman or docker verbs such as rm, run, exec, cp and build.
- The presets are `podman-read` and `systemd-read`; a preset whose binaries are not on PATH is left out, and a contract that asks for it is refused. Files a shim writes itself do not count against the quota, so shims stay read-only.

All commands for a contract run through a **virtual bash** built on the interpreter already in the repo (`mvdan.cc/sh/v3`, see `internal/shell`), but as a strict **allow-list**, not the existing deny-list `BlockFunc`:

- **Package:** new `internal/deck/vsh`. Parse the script to an AST first and reject anything outside the dialect: `eval`, `source`, background `&`, process substitution, here-doc tricks, function and alias definitions, `cd` out of the sandbox, `exec`. Pipes and `&&`/`||` between allowed commands are fine.
- **Exec handler:** a command runs only if it is (a) a pure-Go builtin from the allowed set (`echo`, `cat`, `head`, `tail`, `grep`, `sort`, `uniq`, `wc`, `jq`, ... reusing `internal/shell` coreutils and `jq.go`), or (b) a **service shim**: a fixed binary path plus argv patterns that match exactly (for example `podman ps [--format X]`, `podman logs --tail N <name>` where `<name>` matches the contract targets), executed directly with `os/exec`, no shell, cleared environment, no stdin, time and output caps. Anything else exits `126: not permitted by contract`.
- **Filesystem:** `OpenHandler`/`ReadDirHandler` limited to a per-contract scratch directory with a size quota. No access to the host tree, `~`, config or `~/.deck`.
- **Never grantable:** package runners and fetchers (`bunx`, `npx`, `pnpm dlx`, `uvx`, `pipx`, `pip`, `npm`, `go install`), shells and interpreters, `sudo`/`su`, `curl`/`wget` unless a named HTTP shim is configured, destructive verbs (`rm`, `dd`, `podman rm/run/exec/cp/build`) unless the owner adds them to `max_scope`.
- **How agents use it:** a contract-run agent gets one capability, the `execute` op `sh.run {cmd}` (runs in its contract's vsh) plus `contract.result`. The agent CLI's own tools must be off:
  - Claude: `--strict-mcp-config` (already used) with `--tools` set empty and only the Deck tools allowed.
  - Codex / OpenCode: read-only sandbox and deny rules for bash, edit and web fetch through their config overrides (key names need verifying against the real CLIs).
  - Antigravity: no known way; **fails closed** (contract runs refused).
  - A new capability flag `ContractSafe` per adapter makes the refusal explicit.
- **Honest limits:** this is a command-level sandbox, not an OS container. Shim processes run as Deck's user, so run Deck on the VPS as a dedicated low-privilege (rootless podman) user or in a unit with restricted permissions for defense in depth. The security-critical code is small and fully testable, and Phase 3 builds and attacks it before anything else depends on it.

## 5. Team console (TUI)

One nice shared surface for people and agents.

- **Layout:** full-screen console (key to open, also a docked pane). Header: peer chips with state dots (green connected / amber degraded / grey backoff with countdown), latency, agent counts. Left: peers and agents (status, context %). Center: **feed** of chat, agent status posts, contract cards (proposed / confirmed / running / delivered with result table) and collapsed tool summaries. Bottom: input bar (`chat`, `/contract ...`, `/confirm`, `/revoke`, `/amend`). Contract cards take `y`/`n`/`r` keys.
- **Agents view it** through `console.read` (capped, redacted) and post short status lines with `console.post`; posts are labelled with agent and peer and rendered as text, never as commands.
- **Result view:** delivered results render as tidy tables from the contract schema, so a podman report is readable at a glance.
- **Style and cost:** existing theme tokens, cached renders, no I/O in `Update` (`internal/ui/AGENTS.md`), and a clear "reconnecting in 12 s" line instead of an endless spinner.
- **Persistence:** console feed is an append-only capped log (`~/.deck/peers/console.jsonl`) replicated through the outbox, so a Deck that was offline catches up.

## 6. Thread sync

Each Deck owns its threads (single writer, no conflicts). Peers pull a read-only mirror through the paged `history.read` op into `~/.deck/peers/<peer>/threads/` with a resume cursor, capped by age and size, never merged into the local store. Only threads the owner marks shared are visible. `Manager.Subscribe` (a fan-out tee in `emit`, since `Updates()` has one consumer today) lets the console feed and mirrors stay current.

## 7. Config sketch (`internal/deck/config/config.go`)

```json
"peers": {
  "name": "pc",
  "listen": { "enabled": false, "addr": "0.0.0.0:7722" },
  "share_limits": false,
  "remote": [
    { "name": "vps", "addr": "vps.example.com:7722", "dial": true,
      "role": "contractor",
      "allow_inbound": ["peer.status", "console.read", "console.post", "history.read"],
      "max_scope": ["podman-read"] }
  ],
  "sandbox": { "ttl": "2h", "scratch_mb": 64, "output_kb": 64,
               "presets": { "podman-read": ["ps","logs","inspect","stats","top","events"] } }
}
```
Keys and pinned peer keys live in `~/.deck/peers/`, not in the JSON.

## 8. Phased plan (behind `peers.enabled`)

1. **Transport:** embedded ssh server/client (mcp channel only), identity + pairing + pinning, MCP initialize/tools-list/tools-call with the 2 tools, reconnect supervisor, keepalive, `deck peer pair|add|list|ping|remove`, doctor checks, tests with two in-process instances and a hostile-client test suite (every other SSH request type must be refused).
2. **Peer-scoped ops + outbox:** `hub.Register` with per-caller allow-list, `peer.status`, `console.*`, idempotent outbox, local redaction and caps.
3. **Virtual bash (`internal/deck/vsh`):** AST validator, allow-list exec handler, service shims with argv matching, scratch FS, quotas; built and adversarially tested standalone (injection, metacharacters, path tricks, runner bypass, symlink escape, huge output, fork-like loops).
4. **Contracts:** model, word parser + confirmation, `max_scope`, `contract.*` ops, schema-validated results, adapter lockdown (`ContractSafe`), audit log.
5. **Team console UI:** feed, header, cards, input, results tables, docked pane.
6. **Thread mirror:** `Manager.Subscribe`, paged `history.read`, mirror store.
7. **`deck serve`:** headless engine + listener + drain, cross-process lock on `~/.deck/threads` (reuse `internal/lock`), systemd/podman unit example. Done: it refuses new contracts and shared-thread posts on SIGTERM, waits up to `--drain` for agents and up to 15 s for the outbox, and then stops. It locks `~/.deck/threads/deck.lock` exclusively; terminal Decks take it shared. `console.read` is not in the default allow list: the chat holds what other teammates wrote to this Deck, so the owner allows it per peer; one answer holds at most 100 messages or 32 KB, redacted.

## 9. Risks

- Embedding an SSH server is security-critical: keep it to the single `mcp` channel, pinned keys, rate limits, fuzzing, off by default.
- The virtual bash allow-list must be exact: argv matching, AST validation and tests come before any agent is wired to it. It is a command sandbox, not a kernel sandbox.
- Natural-language contracts: confirmation card is mandatory; agent-originated text can never authorize; result schema is the only exfiltration path and is size- and field-limited.
- Agent CLIs differ in how well their native tools can be disabled; contract runs fail closed on adapters that cannot be locked down.
- Windows identity files need ACL restriction, not only mode bits.
