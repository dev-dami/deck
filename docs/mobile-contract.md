# Deck Mobile: the contract (protocol v3 and relay/v1)

Status: v1 draft, 2026-10-14. Two builders work against this file and the
fixtures in `pkg/remote/wire/testdata/` and nothing else: **A (Go)** writes the
daemon, relay client and queue; **B (TypeScript)** writes the hosted PWA and
the relay Worker. The reasoning is in `.deck/plans/deck-mobile.md`; this file
is what is promised. Anything not written here is the builder's choice.

v3 builds on `docs/remote-contract.md` (v2). Inside the encrypted channel v3
reuses every v2 frame, method, event and entity (v2 §3) with the deltas in §2.
v2 itself stays frozen: the per-PC page keeps working as the offline-LAN
fallback.

## 1. Layers

```
phone ──wss──▶ relay (Cloudflare DO, plaintext envelope §5) ◀──wss── daemon
phone ═══════ Noise channel (§3) carrying Frame3 JSON (§2) ═══════ daemon
phone ──wss://lan-or-tailnet (same Noise channel, opportunistic)──▶ daemon
```

The relay and any network in between see: cid, token and pair-id hashes, sizes,
timing, push endpoints. They never see content.

## 2. Frame3 (inside the Noise channel)

One JSON object per Noise transport message, `{"t": ...}` as in v2. Differences:

- `hello {v: 3, resume?: {epoch, seq, focus?: {project_id, session_id}}}`. No
  token: the device is the Noise initiator static. `Hello3.Validate` checks `v`.
- `req` and `ev` carry **`pj`** (project id) except computer-level methods.
  Every v2 method requires `pj`; an unknown or disabled project is `not_found`.
  v2 rule R10 (ids belong to the named session) holds per project.
- `welcome.server` has `cid`, `name`, `projects: [{id, name, online}]`, and
  features `inbox`, `offline_queue`, `passkey`, `push_e2e`. Never a path.
- Computer-level methods: `projects.list`, `inbox.get`, `inbox.ack {pj,
  session_id}`, `passkey.begin|finish|list|remove`, `assert.challenge
  {action_digest}`, `queue.status`, `push.register`. New events:
  `inbox.changed` (payload `InboxItem[]`, replayed; `unread` is always false in
  it because unread is per device, so a phone calls `inbox.get` for that),
  `receipts.changed`.
- Additive fields: `Approval.tier` (`normal|destructive`) and `.digest`
  (hex sha256 of `kind|command|path|diff`) as `ApprovalV3`;
  `approvals.answer {reason?, assertion?}`; `mode.set {assertion?}`;
  `changes.get {summary: true}` (per file counts plus first hunk <= 4 KiB).
- New error codes: `assertion_required`, `assertion_invalid`, `expired`.
- Deny with a reason: the computer denies, then posts `Denied from phone:
  <reason>` to that agent as a steer message.

Types: `pkg/remote/wire/v3.go`. Fixtures: `testdata/v3_*.json`. Both sides must
decode each fixture and encode it back byte-equal after key sorting (v2 §3.7).

## 3. Noise

`Noise_IK_25519_AESGCM_SHA256`, the phone is the initiator, prologue
`"deck-v3|" + cid`. The responder accepts only an initiator static that is a
pinned, unrevoked, unexpired device. Each direction rekeys (Noise `Rekey()` on
its cipher state) after every 10 000 messages it sends or receives
(`wire.NoiseRekeyMessages`); there is no time-based rekey, since nothing on the
wire would signal it, and reconnecting gives fresh keys anyway.

**Transport framing.** A Noise message is at most 65535 bytes, so a frame is cut
into chunks of at most 65518 bytes. Each chunk is prefixed with one flag byte
(0 more follows, 1 last chunk; an empty frame is one chunk with just the flag)
and Noise-encrypted as its own transport message. Messages travel as relay
binary frames in order. A reassembled frame over `wire.MaxFrameBytes` (12 MiB)
is an error. Go: `pkg/remote/e2e`; vector: `noise_frames.json` (lengths and
SHA-256 of each ciphertext chunk; keys and seeds as in `noise_ik.json`).

- **Pairing:** `Noise_IKpsk2_25519_AESGCM_SHA256`, same prologue, the PSK from
  the QR. Phone payload `PairHello`, computer payload `PairWelcome` (device id,
  `relay_token`, expiry). A wrong PSK fails at message 2.
- **Queued actions and lock-screen answers:** `Noise_K_25519_AESGCM_SHA256`,
  one message, both statics known. The payload is a `QueuedAction`.
- **WebCrypto only on the phone:** X25519, AES-GCM, SHA-256 HMAC/HKDF. No
  crypto library in the PWA. Go uses `github.com/flynn/noise`.
- **Vectors:** `testdata/noise_ikpsk2.json`, `noise_ik.json`, `noise_k.json`.
  Every key is fixed (the ephemeral is a fixed seed fed to the DH keygen). B's
  Noise must reproduce each `messages[].ciphertext_hex`, `handshake_hash_hex`
  and `transport[].ciphertext_hex` byte for byte. Regenerate with
  `go test ./pkg/remote/wire -update-v3` (changes need both sides' agreement).

## 4. Pairing

1. `deck remote pair` arms one slot: 32 random bytes `psk`, 5 minutes
   (`PairCodeTTL`), one at a time, burned after `PairFailuresToBurn` failures or
   one success. The daemon sends the relay `pair_open`.
2. The QR is `https://app.<domain>/#p=<base64url(QRBlob JSON)>`
   (`v3_entity_qr_blob.json`). Fragment only; the page strips it from history
   first. `lan` and `fp` are optional hints for the direct path.
3. The phone opens `wss /r/v1/c/:cid/pair`, sends `PairOpen {pair_id}`, then
   runs the IKpsk2 handshake. The pair id is unpadded base64url of
   HKDF-SHA256(ikm = psk, salt = none, info = "deck-pair-id", 32 bytes). The
   relay is given `pair_id_hash` = lowercase hex SHA-256 of the pair id string
   (UTF-8 bytes) and compares that.
4. The computer stores the phone static as a **pending** device (not accepted
   by any connection path), mints `device_id` and `relay_token`, and returns
   them in `PairWelcome` (message 2). Because the PSK is mixed at the end of
   message 2, only a holder of the secret can read it. The phone then sends one
   `hello {v: 3}` frame as its first transport message; only that confirms the
   device, spends the slot, tells the relay the new token hashes and announces
   the pairing on the desktop. No confirmation within 10 s, or a bad
   handshake, removes the pending device and counts as a failure; three
   failures burn the slot.

## 5. Relay protocol `relay/v1`

Plaintext envelope; types in `pkg/remote/wire/relay.go`, fixtures
`relay_*.json`.

- `POST /r/v1/register {relay_pub}` -> `{cid}`. **Encodings (all fixed):**
  `relay_pub` is the **raw 32-byte Ed25519 public key**, unpadded base64url
  (43 characters); `cid` is the first 22 characters of the unpadded base64url of
  SHA-256 over those 32 raw bytes (not over an SPKI/DER wrapper); the host's
  `auth.sig` is the 64-byte Ed25519 signature, unpadded base64url (86 characters);
  the signed message is the UTF-8 bytes of `"deck-relay-v1|" + cid + "|" + nonce`
  where `nonce` is the challenge string exactly as sent. Binary payloads inside
  JSON frames (`b64` in `data`, `mail`, `snap`, `push`, and `MailPost.b64`) are
  standard base64 with padding. Tokens and pair ids are hashed as the UTF-8 bytes
  of the string and written as lowercase hex SHA-256. Rate limited per IP.
- **Host** `wss /r/v1/c/:cid/host`: relay sends `challenge{nonce}`; host answers
  `auth{sig}` = Ed25519 over `"deck-relay-v1|" + cid + "|" + nonce`; relay sends
  `ready`. A newer host replaces an older one (close 4409). Host frames are
  `HostFrame` JSON: `devices{hashes}`, `pair_open{pair_id_hash, exp}`,
  `pair_close`, `open{lane, device|pair}`, `data{lane, b64}`, `close{lane,
  code}`, `mail{id, device, b64}` / `ack{id}`, `snap{device_hash, b64}`,
  `push{device_hash, b64, ttl, urgency}`.
- **Who opens lanes.** The **relay** allocates a lane id when a phone has
  authenticated (a valid token on `dev`, a live pair window on `pair`) and sends
  the host `open{lane, device}` or `open{lane, pair: true}`; the host never sends
  `open` (the relay ignores it). Each phone connection gets its own lane. A phone
  that arrives while no host is connected waits, and gets its lane the moment one
  authenticates. When the host connection drops, every lane on it is dead, so the
  relay closes those phones with code 1012 and they reconnect. A phone that
  leaves makes the relay send `close{lane}` and frees the lane (8 per device).
- **Host replacement.** A host socket only replaces the current host after it
  answers its own challenge correctly (then the old one is closed 4409). Sockets
  that never answer, or answer wrongly, change nothing: knowing a cid must not be
  enough to knock a computer offline.
- `GET /r/v1/vapid` returns `{public_key}` (the relay's VAPID public key,
  unpadded base64url, the `applicationServerKey` a phone subscribes with) or 404
  when push is not configured.
- **Push endpoints** are accepted only for https, the default port, no
  credentials, and a known push service host (FCM, Mozilla autopush, Apple,
  Windows). The relay checks again before it posts and never follows redirects.
- **Phone** `wss /r/v1/c/:cid/dev`: first text frame `PhoneAuth {token}` (never
  in the URL), checked against the stored token hashes before the Durable
  Object is reached; then binary frames are Noise messages. `wss
  /r/v1/c/:cid/pair`: first frame `PairOpen`.
- `POST /r/v1/c/:cid/mail` (`MailPost`, bearer relay token), `GET
  /r/v1/c/:cid/snap` (`SnapResponse`), `PUT /r/v1/c/:cid/push` (`PushPut`),
  `GET /r/v1/c/:cid/status` (`ComputerState`). The mail's `expires_at` is
  copied out of the sealed action so the relay can drop it unread.
- **Limits:** frame <= 1 MiB, mail <= 64 KiB, snapshot <= 256 KiB, mail TTL =
  `expires_at` (<= 24 h) or 7 d for receipts, 1000 mails and 16 MiB per cid, 8
  lanes per device. Close codes: v2's 4401, 4403, 4429, plus 4404 unknown cid
  and 4409 host replaced.
- The relay also serves the PWA (`dist-hosted`) from the same origin, so CSP
  `connect-src 'self'` holds.

## 6. Offline queue

Queueable (`QueuedAction.Validate` enforces it): `chat.send`, `session.new`,
`chat.continue`, `models.set`, `approvals.answer` (bound), `queue.cancel`.
**Never:** `agent.interrupt`, `chat.cancel`, `mode.set` / yolo.

- Default expiry 8 h, the phone offers 1, 8 or 24 h (max 24 h), cap 20 per
  device, applied qids kept 7 days.
- A queued approval carries `precond {approval_id, digest}` and runs only if
  that approval is still pending with the same digest. A destructive allow also
  carries an `Assertion` over `qid` and the digest. Otherwise it is dropped with
  a receipt (`approval_gone`, `digest_mismatch`, `assertion_invalid`).
- On wake the daemon drains the whole mailbox, applies cancels, sorts each
  device FIFO by `created_at`, dedups by `qid`, and runs. Delivery is
  at-least-once, effect exactly-once. Mail is acked after the receipt is stored.
- Attached TUIs show a 30 s veto banner (`QueueVetoWindow`) first; with no TUI
  the actions run at once.
- The computer writes an encrypted `Snapshot` (inbox, pending approvals, last
  50 receipts) to the relay per device, debounced 10 s and whenever the relay
  link comes up. The phone shows it greyed with "offline <age>" from
  `ComputerState.last_seen`. A snapshot is **one Noise K message** with the
  computer as initiator and the phone as responder (the phone opens it with the
  computer's pinned static; prologue as in §3), so its JSON is at most 60 000
  bytes: the computer drops approval bodies first, then older receipts, then
  the inbox tail to fit.
- **Mail handling (computer):** mail names its device by relay token hash. The
  computer opens it with that device's pinned static (Noise K, phone as
  initiator) and drops, but still acks, anything that does not authenticate,
  names a different device, or fails `QueuedAction.Validate`. A queue already
  holding 20 actions for the phone, or an `expires_at` further than 24 h plus
  5 min ahead, is answered with a `dropped` receipt (`busy`, `expired`). Mail is
  acked only after the action is stored. Execution waits until the mailbox has
  been quiet for 2 s, then runs cancels, then everything else in `created_at`
  order. An action for a project whose Deck is not running keeps waiting until
  it starts or the action expires (then `dropped`/`expired`).
- **Receipt reasons:** `expired`, `session_gone`, `approval_gone`,
  `digest_mismatch`, `cancelled`, `vetoed`, `busy`, `project_disabled`,
  `assertion_invalid`, `duplicate`; a `failed` receipt carries `failed` or
  `invalid`.

## 7. Approval tiers

| Action | Needs |
|---|---|
| read, prompts, continue, interrupt, model switch, deny, allow (tier normal) | Noise session |
| allow / allow_session on a `destructive` approval | + fresh WebAuthn assertion |
| `mode.set yolo` / yolo grant | + device `allow_yolo` + fresh assertion |

WebAuthn: `rp.id = app.<domain>`, user verification required, attestation
`none`, algorithms ES256 and EdDSA. The computer verifies; the phone never
decides a tier.

- **Register:** `passkey.begin` returns `{challenge, rp_id, user_id}` (random
  32-byte challenge, valid 5 min, one per device); the phone runs
  `navigator.credentials.create` and sends `passkey.finish {credential_id,
  client_data_json, attestation_object}` (all base64url). At most 5 passkeys
  per device.
- **Assert:** for an action the phone computes its **action digest**:
  - approval: `ApprovalV3.digest`;
  - yolo for a session: lowercase hex SHA-256 of `"mode.set|yolo|" + pj + "|" +
    session_id`.
  It calls `assert.challenge {action_digest}` and gets a `nonce` (valid 2 min,
  single use). The challenge it passes to `credentials.get` is
  `SHA-256("deck-assert-v1" || 0x00 cid || 0x00 device_id || 0x00 action_digest
  || 0x00 nonce)` (vector: `assert_challenge.json`). It then sends the
  assertion in `approvals.answer.assertion` or `mode.set.assertion`. A queued
  action uses its `qid` as the nonce and needs no round trip.
- **Counter:** a good assertion advances the stored counter; a counter that
  does not advance is rejected (`assertion_invalid`), except an authenticator
  that always reports 0.
- **Errors:** no assertion -> `assertion_required`; no live nonce for the digest
  -> `expired`; wrong passkey, origin, relying party, missing user verification,
  bad signature or replay -> `assertion_invalid`.
- **Tiers:** the classifier (`pkg/remote/policy`) marks destructive: recursive
  deletes, force pushes, `git reset --hard`, `git clean`, `git checkout -- .`,
  `git branch -D`, SQL drop/truncate/delete, `mkfs`/`dd`/`format`, recursive
  `chmod`/`chown`, `sudo`, pipe-to-shell, publishes, redirects to system paths,
  anything with substitution/`eval`/`xargs`/`find -exec|-delete`, truncated
  commands, edits outside the project root or to `.env*`, `*.pem`, `*.key`,
  `id_*`, `.git`, `.ssh`, `.aws`, and every approval kind it cannot parse.

### Native Android passkey origins

Native Android Credential Manager produces `clientDataJSON.origin` as
`android:apk-key-hash:<unpadded-base64url-SHA256-of-signing-certificate>`.
The daemon accepts it only when the complete canonical value is explicitly
configured in `mobile.json`'s `android_origins`, or with
`deck mobile setup --android-origin VALUE` (repeatable). It never accepts a
wildcard or uses an assertion's own origin as a trust configuration.
RP ID hash, challenge, credential signature, user presence/verification and
cross-origin rejection still apply. The original HTTPS browser origin remains
valid. OS app/domain associations must separately be provisioned.

## 8. Push

The phone registers with `push.register {p256dh, auth, approvals, done}` over
the E2E channel (empty keys unregister) and puts its push **endpoint** on the
relay with `PUT /r/v1/c/:cid/push`. The computer encrypts each push itself
(RFC 8291, aes128gcm, one record; Go test checks the RFC's appendix A example
byte for byte) and hands the ciphertext to the relay (`push` host frame), which
adds VAPID and posts it. The relay and the push service never see content. The
browser decrypts natively, so there is no client vector.

The plaintext is a `PushMessage` (fixtures `v3_push_approval`, `v3_push_done`):

- New messages include `cid`, the sending computer's id. The hosted service
  worker matches it to the stored computer before offering actions; it never
  guesses from the project or session id. Older messages without `cid` can
  open the app but cannot be answered from a notification.

- `kind: "approval"` when an approval is requested and the phone asked for
  approvals; urgent, 1 h TTL. It carries `approval_id`, `digest` and `tier`. For
  `tier: "normal"` the service worker may offer Approve and Deny actions that
  send a sealed queued `approvals.answer` (precond = that id and digest); for
  `destructive` it must only open the app.
- `kind: "done"` when a session stops with nothing pending and the phone asked
  for done; normal urgency, 6 h TTL.
- A phone with a live session is not pushed to.

The daemon IPC (A4) is Go-internal and not part of this contract.

Any change to §2 to §7, the types, fixtures or vectors goes through a contract
change both sides acknowledge.
