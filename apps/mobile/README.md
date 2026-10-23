# Deck native mobile client

This package is a React Native application built with Expo SDK 57. Screens use
native components and native stack navigation. There is no WebView. It shares
the v3 wire protocol and Noise implementation with `pkg/remote/web/src`; Metro
resolves those sources using this package's native dependencies.

The primary target is Android. iOS configuration is included. A custom
development build is required because Quick Crypto/Nitro and passkeys contain
native code; Expo Go cannot run this app.

## Install and validate

Use Node 22.13 or newer and Bun 1.3.14 or newer. From the repository root:

```sh
cd apps/mobile
bun install --frozen-lockfile
bun run check
bun run test
bunx expo install --check
bun run doctor
bun run export:android
```

`export:android` checks production bundling and creates JavaScript/assets in
`dist/`. It does not compile native C++/Kotlin, produce an APK, or validate a
device's cryptography and passkey behavior.

The entry point installs native crypto before loading application modules.
Crypto capability checks must pass on a real development build before a phone
can pair. A JavaScript-only test runner cannot prove native key behavior.

## Build an installable Android APK

Local builds require Android Studio/SDK, a compatible JDK, and an emulator or
USB device with debugging enabled. Generate the Android project and compile:

```sh
bun run prebuild:android
cd android
./gradlew assembleRelease
```

On Windows use `gradlew.bat assembleRelease`. The generated native project is
ignored and can be regenerated from app config. Its default local release
configuration uses a development signing key; configure your release signing
before distributing outside a test group. The APK is generated under
`android/app/build/outputs/apk/release/` after a successful Gradle build.

For development with Metro, run `bun run android` and `bun run start` when you
want to start a development server. Do not use a development client APK as a
standalone preview: it expects Metro to provide JavaScript.

The checkout is now linked to `@erioluwa-dev/deck-mobile`, project
`6da6d2a1-0650-421e-a017-f4ef3b57fed0`. After logging in with access to it:

```sh
bunx eas-cli login
bunx eas-cli build --platform android --profile preview
```

The `preview` profile produces an internally distributed, standalone APK with
JavaScript embedded. No login secrets or signing credentials are stored in
this repository. Expo holds the Android signing credentials;
the public account/project identifiers are in `app.config.ts`.

The repository-root `.easignore` preserves the app's relative path and the
shared sources in `pkg/remote/web/src`, plus four public crypto vectors.
It excludes dependencies, generated native projects/bundles and local secrets.
Run EAS commands from `apps/mobile` so the build uses the correct app directory.

## Pairing and native passkeys

Run `deck mobile pair` on a configured computer, then scan its QR from the app.
The `deck-mobile` custom scheme is registered for explicit native links; normal
HTTPS pairing URLs can be scanned or pasted without claiming a hosted domain.

For native passkeys, set `DECK_MOBILE_PASSKEY_DOMAIN` to your actual RP/relay
hostname before generating/building the native app. The config then adds iOS
`webcredentials`/`applinks` associations and Android verified HTTPS app links.
An environment variable alone does not provision the required domain files:

- Host `/.well-known/assetlinks.json` with package `com.deckcli.mobile`, the
  actual signing certificate SHA-256 fingerprint, and both
  `delegate_permission/common.get_login_creds` and
  `delegate_permission/common.handle_all_urls` relations.
- For iOS host `/.well-known/apple-app-site-association` with your real Apple
  team id and bundle identifier in `webcredentials.apps`.
- Configure the daemon's allowed native Android origin using that same signing
  certificate. A browser origin does not authorize native Android assertions.

No domain or signing fingerprint is guessed. Change the provisional application
identifiers before the first public release if another identifier is required.

On macOS with Xcode installed, `bun run ios` builds an iOS development app.
iOS signing and a native passkey/device run remain platform-specific checks.

## Current verification limits

The implementation currently passes 40 native logic/storage/crypto-boundary
tests, TypeScript and Biome checks, Expo dependency validation, and all 21 Expo
Doctor checks. Android prebuild and Android/iOS production JS exports pass.
The shared web regression suite passes 585 tests. These host tests exercise
real Noise handshakes and secure-storage boundaries using injected host
providers; they do not execute a phone's C++ JSI or SecureStore implementation.

The local `gradlew.bat assembleRelease` attempt stops because Java/JAVA_HOME
is missing. EAS cloud compilation and signing subsequently succeeded on
2026-10-15, producing a standalone Android preview APK:
[download Deck](https://expo.dev/artifacts/eas/3kdC27S_QbIDLIg7lEYMA7Nzr4CLzbFBDR5FS3N30FU.apk).
The downloaded archive passes CRC checks and includes its JS bundle and native
crypto libraries. Installation and runtime acceptance on a phone remain untested.

Before release, measure cold startup, long-list scrolling, streaming responses,
memory and foreground recovery on a release build and a representative phone.
No FPS, startup-time or battery-performance claim has been measured here.

The existing daemon/relay notification path sends Web Push for the browser
client. Native FCM/APNs delivery and native background notification actions
are not implemented by this package; the native app currently uses foreground
connections and refresh. A preview APK does not automatically add native push.

The development machine used to scaffold this app has no Java/Android SDK or
ADB, so a local Gradle build, installation and real-device native module checks
have not been performed. Exporting JavaScript successfully is separate from
those required device checks.

## Configuration and branding

### Local Wi-Fi preview

[Download the LAN APK (0.1.1)](https://expo.dev/artifacts/eas/yKjQULjK5Ow5nGBhFzJ2bo7VskDbPSCkUYadbOkICF0.apk).
The EAS build completed successfully on 2026-10-16. Follow
[the local Wi-Fi guide](../../docs/mobile-lan.md) for the companion desktop
update, relay startup, firewall configuration and QR pairing.

The single `preview` EAS profile builds version 0.1.1 (Android version code 2) with
`EXPO_PUBLIC_DECK_LAN_HTTP=1`. It permits HTTP relay origins only for literal
private IPv4 addresses (10/8, 172.16/12 and 192.168/16), plus existing loopback
support. Production/ordinary preview builds retain HTTPS requirements. The
Android manifest plugin enables HTTP/WS only for this build; it does not
disable TLS certificate verification. Noise still encrypts messages end to end,
but relay authentication tokens and connection metadata lack TLS protection:
use plain-HTTP relays only on a trusted private network. HTTPS relays work too. Native passkeys still
require a verified domain and signing-certificate association.

Build with `eas build --platform android --profile preview`. Use the companion
desktop build, which adds `deck mobile setup --allow-lan-http --relay
http://YOUR_PC_PRIVATE_IPV4:8787`. Restart the daemon after changing its relay.
The PC and phone must share a reachable Wi-Fi network, and the relay's port
must be allowed by Windows Firewall. Guest/client isolation can block access.

`app.config.ts` owns native permissions, association domains and app identity.
Camera access is used only for QR scanning; Android audio recording permission
is disabled. Native projects are generated rather than hand-maintained.

`bun scripts/generate-assets.ts` regenerates the native launcher assets from
the same card geometry used by the existing Deck web client.

References: [Expo SDK compatibility](https://docs.expo.dev/versions/latest/),
[Android APK profiles](https://docs.expo.dev/build-reference/apk/),
[Quick Crypto installation](https://github.com/margelo/react-native-quick-crypto#installation),
[native passkey associations](https://github.com/peterferguson/react-native-passkeys#android-setup).

## Native origin configuration and storage

Convert your actual signing certificate's SHA-256 fingerprint (32 bytes,
not the hex text) to unpadded base64url, then configure the daemon:

```sh
deck mobile setup --android-origin 'android:apk-key-hash:YOUR_BASE64URL_SHA256'
```

Repeat the flag for each certificate you explicitly trust. The supplied list
replaces the previous list. Restart the daemon afterwards. Wildcards and
ordinary HTTPS URLs are refused by this flag; the existing HTTPS browser
origin checks remain in place. Domain associations must still be provisioned.

Native session settings support passkey registration/removal, Plan/Ask modes,
and a confirmed passkey-verified yolo change for phones with the computer-side
grant. Offline destructive approval is unavailable; normal answers and
destructive denials can queue with digest preconditions.

Private identity material, relay tokens and cache keys stay in SecureStore.
SQLite contains public metadata, sealed outgoing actions and AES-GCM-encrypted
snapshots. Snapshot encryption binds each entry to its computer. Session event
subscriptions are released when screens detach. Queue delivery/cancellation
are serialized per computer; cancellation after delivery remains a request
and cannot undo an action that already began.
