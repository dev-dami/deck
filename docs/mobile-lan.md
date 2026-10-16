# Local Wi-Fi testing (Android)

Use the **LAN** APK (0.1.1, Android code 2) and the companion desktop build
`.deck/builds/deck-mobile-connect.exe`. The earlier 0.1.0 APK cannot use private
IPv4 HTTP relay origins. Install the new APK over it; the package and signing
key are unchanged. Expo Go and USB debugging are not required.

This is a trusted-network test configuration. Noise encrypts messages, but
HTTP exposes relay authentication tokens and metadata. Do not expose this
development server to the internet or forward its port through your router.
Wrangler also exposes local development tooling. Native passkeys require a
verified domain and are not enabled by this LAN setup.

## 1. Update the desktop

The latest desktop source also supports one-step remote control. Once running
that build, type `/mobile` (or `/remote`) in Deck to open the remote-control
panel. Press **l** to start the local Wi-Fi relay and daemon, share the current
project, and show a pairing QR. Press **s** to reuse the saved relay settings,
**p** for a fresh QR, **r** for status, and **x** to stop services that the
panel started. If the QR will not fit, enlarge the terminal or press **c** and
paste the private link into the phone app. Separately launched relay/daemon
processes are reused where possible and are not stopped by this panel.

The equivalent one-step PowerShell command, from your project directory, is:

```powershell
deck mobile connect --lan --project .
```

This replaces the manual setup/relay/daemon/pair sequence below. It still needs
Node.js and this checkout's installed Worker runtime. The first run locates
the runtime from the project/executable ancestors; if needed, pass
`--relay-dir C:\Users\Erioluwa\Downloads\deck\pkg\remote\relay`.
The location is remembered for later use from other projects. Windows Firewall
or Wi-Fi client isolation may still require the network checks below.

Close Deck first, then run each command separately in PowerShell:

```powershell
$deckBin = "$env:LOCALAPPDATA\deck\bin"
Copy-Item "$deckBin\deck.exe" "$deckBin\deck-before-lan.exe"
Copy-Item "C:\Users\Erioluwa\Downloads\deck\.deck\builds\deck-mobile-connect.exe" "$deckBin\deck.exe" -Force
deck mobile setup --help
```

Help must include `--allow-lan-http`. To rebuild the companion from source:

```powershell
cd C:\Users\Erioluwa\Downloads\deck
go build -o .deck/builds/deck-mobile-connect.exe .
```

## 2. Start the local relay

Connect both devices to the same trusted Wi-Fi. Use `ipconfig` to find the
PC's **Wi-Fi IPv4 address**, not a virtual adapter or loopback address. The
address observed on this machine during verification was `192.168.1.10`;
replace it below if it has changed.

In a PowerShell window that you keep open:

```powershell
cd C:\Users\Erioluwa\Downloads\deck\pkg\remote\relay
node node_modules/wrangler/bin/wrangler.js dev --local --ip 192.168.1.10 --port 8787
```

The existing installed Wrangler is used. A fresh checkout needs its relay
dependencies installed first. This runs local Durable Objects; no Cloudflare
login or deployment is required. See the official
[Wrangler dev options](https://developers.cloudflare.com/workers/wrangler/commands/workers/).

If Windows blocks access, ensure this trusted Wi-Fi has the Private network
profile. In an elevated PowerShell window, an example narrow firewall rule is:

```powershell
New-NetFirewallRule -DisplayName 'Deck LAN relay 8787' -Direction Inbound -Action Allow -Protocol TCP -LocalPort 8787 -LocalAddress 192.168.1.10 -RemoteAddress LocalSubnet -Profile Private
```

Open `http://192.168.1.10:8787` in the phone's browser to check reachability.
Use the native app for pairing. If the page cannot be reached, check the IP,
firewall and guest-network/client-isolation settings before trying a QR.

## 3. Configure Deck and pair

In another PowerShell window:

```powershell
deck mobile setup --allow-lan-http --relay http://192.168.1.10:8787 --name 'My PC'
deck mobile projects add C:\Users\Erioluwa\Downloads\deck --name 'Deck'
deck mobile daemon
```

Keep that daemon window open. If an earlier daemon is already running, stop
it in its original terminal before starting this one, since configuration is
read at startup. In a third window:

```powershell
deck mobile status
deck mobile pair
```

Status should say the relay is online. Open the Android app, scan the QR,
check the displayed PC/relay, and confirm. Keep `pair` running until it reports
success. The QR expires after five minutes and can be used once. Open Deck on
the shared project to test live sessions. `deck mobile devices` lists phones.

## Stop testing

Stop the foreground daemon and relay with Ctrl+C in their windows. If you
added the example firewall rule, remove it from elevated PowerShell:

```powershell
Remove-NetFirewallRule -DisplayName 'Deck LAN relay 8787'
```

Switching back to an HTTPS relay can disable the desktop LAN opt-in explicitly:
`deck mobile setup --allow-lan-http=false --relay https://YOUR_REAL_RELAY`.
New relay addresses may require re-pairing. A changed PC DHCP address requires
updating the relay bind address/configuration and possibly the firewall rule.

## Verification boundary

The real local Worker, Go daemon and shared TS phone protocol were tested
together using isolated state: new-host registration, IKpsk2 pairing,
authenticated IK session and `inbox.get` passed. Android manifest generation
was checked with LAN enabled and disabled. Physical Android Wi-Fi access,
native crypto, camera scanning and UI acceptance still require device tests.
