# Deploy Deck Mobile

Deck Mobile is a **TypeScript + Preact progressive web app**, built with Bun.
It runs in a browser and can be installed on a phone's home screen; it is not
a React Native or Expo app. The desktop app, mobile daemon and CLI are Go.
The TypeScript Cloudflare Worker uses one SQLite-backed Durable Object per
computer to relay WebSockets, encrypted mailbox entries, snapshots and push.

These are deployment instructions, not a record of a completed deployment.
Live Worker/Go interoperation, real phone pairing, WebAuthn, notification
delivery and OS autostart still need staging verification.

## Origin and configuration

Use one HTTPS origin at the root, for example `https://deck.example.com`, for
both the hosted app and `/r/v1/*`. The current `pkg/remote/relay/wrangler.jsonc`
already bundles `../web/dist-hosted` as Worker static assets. Build the hosted
target before deploying; `web/dist` is the separate embedded v2 client.

The CLI's `--relay` address also determines the pairing URL, passkey RP ID and
expected WebAuthn origin. There is no independent frontend-origin setting.
The relay does not implement cross-origin HTTP preflights or CORS response
headers. Hosting the frontend on another origin therefore is not supported
by configuration alone. The frontend CSP permits HTTPS/WSS connections, but
that does not override CORS or WebAuthn origin verification. A reverse proxy
can provide one public origin if it forwards all `/r/*` requests, including
WebSocket upgrades, to the relay and serves the app at `/`.

Before deployment, review these fields in `wrangler.jsonc`:

- `name`: select the intended Worker; the checked-in name is `deck-relay`.
- `durable_objects.bindings`: keep `COMPUTER` pointing to `ComputerDO`.
- `migrations`: the initial `v1` migration creates the SQLite class. Preserve
  migration history for an existing deployment; do not rename the class.
- `assets.run_worker_first: ["/r/*"]`: keep API and socket routes out of the
  SPA HTML fallback, including browser navigation requests.
- `vars.VAPID_SUBJECT`: replace the placeholder with your contact `mailto:`
  address or HTTPS contact URL.
- `compatibility_date`: currently `2025-09-01`. Change deliberately and rerun
  the relay suite when adopting newer runtime behavior.

Use a separate Worker/config/account for staging so its device state and
mailboxes cannot overlap production. This repository does not currently
define `env.staging`; do not assume `--env staging` is configured.

Cloudflare documents selective asset routing in [Worker-first routing](https://developers.cloudflare.com/workers/static-assets/routing/worker-script/).

## Local checks and builds

From the repository root, with the Go version required by `go.mod` installed:

```sh
go build ./...
go test ./pkg/remote/... ./internal/cmd
go vet ./pkg/remote/... ./internal/cmd
```

In `pkg/remote/web`:

```sh
bun install --frozen-lockfile
bun run check
bun run test
bun run build.ts --target hosted
```

In `pkg/remote/relay`:

```sh
bun install --frozen-lockfile
bun run check
bun run test
bunx wrangler deploy --dry-run --outdir .wrangler/dry-run
```

The dry run bundles locally; it does not deploy or validate a live account's
permissions. A successful unit suite does not establish browser-to-Worker-to-Go
interoperation. Existing Windows relay runs can print temporary-directory
cleanup warnings after tests; inspect the actual test result and exit code.

For an explicitly authorized local interoperation session, run
`bunx wrangler dev` from the relay directory, then configure a separate test
desktop profile against the printed loopback URL. The Go configuration allows
HTTP only for loopback. A physical phone opening a PC's LAN HTTP address is
not equivalent to a secure origin; use HTTPS staging for device tests.

## Push key material

Push is optional. Without keys, `GET /r/v1/vapid` returns 404 and notification
subscription fails, while ordinary pairing and chat can still work.

The Worker expects these exact values:

| Setting | Encoding |
| --- | --- |
| `VAPID_PUBLIC_KEY` | Unpadded base64url of the 65-byte uncompressed P-256 public point |
| `VAPID_PRIVATE_KEY` | Unpadded base64url of the UTF-8 JSON **private JWK**, including `kty`, `crv`, `x`, `y`, `d` |
| `VAPID_SUBJECT` | Contact URI in Worker vars |

The private setting is not the conventional raw 32-byte VAPID secret. Generate
a matching pair once and retain it securely; rotating it requires browsers to
subscribe again. One browser subscription can serve several computers using
the same relay key; subscribing through another relay with a different key is
explicitly refused to protect existing subscriptions.

To generate the expected values without printing secret contents, save the
following as `generate-deck-vapid.mjs` outside the repository and run it with
Node. It prints only a temporary file's path. Keep that directory private to
your OS user (POSIX permissions are requested by the script; on Windows check
the inherited ACL).

```js
import { webcrypto } from "node:crypto";
import { mkdtemp, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";

const pair = await webcrypto.subtle.generateKey(
  { name: "ECDSA", namedCurve: "P-256" }, true, ["sign", "verify"],
);
const publicPoint = await webcrypto.subtle.exportKey("raw", pair.publicKey);
const privateJwk = await webcrypto.subtle.exportKey("jwk", pair.privateKey);
const encode = (value) => Buffer.from(value).toString("base64url");
const directory = await mkdtemp(join(tmpdir(), "deck-vapid-"));
const file = join(directory, "secrets.json");
await writeFile(file, JSON.stringify({
  VAPID_PUBLIC_KEY: encode(publicPoint),
  VAPID_PRIVATE_KEY: encode(JSON.stringify(privateJwk)),
}), { mode: 0o600 });
console.log(file);
```

Import that file with `bunx wrangler secret bulk /path/to/secrets.json` from
the relay directory, after selecting and authenticating the intended Worker
account. Store a secure backup and remove the temporary copy after import.
Do not commit or print the private key, subscription auth secrets or relay
tokens. The secret command changes Cloudflare state and belongs to the
authorized deployment step. See [Wrangler commands](https://developers.cloudflare.com/workers/wrangler/commands/).

## Authorized deployment

These commands publish/change remote resources. Run them only for the intended
account, Worker and domain after the local checks and config review:

```sh
bunx wrangler login
bunx wrangler whoami
bunx wrangler deploy
```

Import push secrets as described above. Attach the intended custom domain
using Cloudflare's Worker settings, or deliberately use the HTTPS workers.dev
origin printed by deployment. Use that same stable origin in desktop setup;
switching the frontend hostname changes passkey scope and browser storage.
Do not publish just `dist-hosted` to an unrelated Pages hostname and expect
the current CLI pairing/passkey flow to follow it.

With the built `deck` binary available on PATH:

```sh
deck mobile setup --relay https://deck.example.com --name "My computer"
deck mobile projects add /absolute/path/to/project --name "My project"
deck mobile daemon --detach --log
deck mobile status
deck mobile pair
```

If an old daemon is already running, restart it after changing configuration.
Keep `deck mobile pair` running while scanning its single-use QR code. Open
the project in Deck for its live terminal provider; the daemon can also spawn
a headless host for shared projects. Do not expose local IPC ports publicly.

After staging verification, `deck mobile install` enables OS autostart;
`deck mobile uninstall` removes it. OS-specific scheduled task, login and
service behavior requires testing on the target machine.

## Deployment acceptance checklist

Record device/browser versions, URLs, timestamps and observed results:

1. `/` serves the hosted app; `/sw.js` serves JavaScript; the manifest and
   icons load. `/r/v1/vapid` serves `{public_key}` if push is configured.
   Navigating to an unknown `/r/*` returns an API error rather than index HTML.
2. `deck mobile status` reports the relay connected. Scan a fresh QR from a
   real phone, confirm pairing, and verify the QR secret leaves the address
   bar. Check `deck mobile devices` and the phone's computers list.
3. Open an opted-in project/session, exchange messages and interrupt a live
   agent. Confirm disabled/unshared projects are inaccessible.
4. Exercise normal approve/deny and destructive approval with a registered
   passkey. A wrong origin, stale approval or failed biometric must not allow
   the action. Test deny-with-reason and inspect the agent's received message.
5. Disconnect the PC, view the stale snapshot, queue a prompt, reconnect and
   inspect its receipt. Test expired/stale actions and desk-side queue cancel.
6. Drop and restore the phone network. Verify reconnect, current inbox and
   transcript, without duplicate messages or duplicate queued actions.
7. Enable notifications from the phone's button. Close/disconnect its live
   session (connected phones are not pushed). Trigger approval and completion
   notifications. Normal-tier action buttons, where the OS exposes them, must
   target the correct computer and bind approval ID/digest. Destructive pushes
   must open the app. On iPhone, test the installed home-screen app and app-open
   flow; do not promise lock-screen approval buttons.
8. Revoke the phone with `deck mobile revoke DEVICE_ID`. Verify its socket
   drops, reconnect fails and queued/notification actions cannot run.

No items above should be marked verified solely from mocks, Go fixtures or a
dry-run deployment. Keep the existing v2 embedded client checks separate from
hosted v3 staging results.

## Checks performed while preparing this runbook

On 2026-10-15, the hosted build, TypeScript check and focused push suite
(6 tests) passed. Biome accepted the Worker routing configuration. Wrangler
4.151.0 completed `deploy --dry-run`, bundling the Worker and finding both
`COMPUTER` and `ASSETS` bindings. The dry run used the local hosted asset
directory; it did not start a server, publish assets, configure secrets or
establish real-device operation. Use the task's current status log for the
broader suite results.
