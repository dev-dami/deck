# Native platform boundaries

`passkeys.ts` uses the real `react-native-passkeys` Expo module: named
`create`, `get` and `isSupported` APIs. It sends OS-produced client data,
authenticator data, attestation and signature unchanged to the Go verifier.
Cancellation never approves an action. A biometric prompt alone is not an
assertion. This requires a signed custom development/release build, not Expo Go.

Set the relay's real domain when building and hosting associations:

- iOS: add `webcredentials:RELAY_DOMAIN` to associated domains and serve
  `https://RELAY_DOMAIN/.well-known/apple-app-site-association` with a
  `webcredentials.apps` entry of `APPLE_TEAM_ID.BUNDLE_IDENTIFIER`.
- Android: serve `https://RELAY_DOMAIN/.well-known/assetlinks.json` for the
  app package and actual signing certificate SHA-256 fingerprint. Include
  `delegate_permission/common.get_login_creds`; app-link permission alone
  does not authorize passkeys. Debug and release certificates differ.
- Android client data uses `android:apk-key-hash:` followed by the unpadded
  base64url SHA-256 hash of the DER signing certificate. Opt in to that exact
  origin on each computer with `deck mobile setup --android-origin ORIGIN`
  and restart its daemon. Do not replace the native origin with the relay's
  HTTPS origin, and do not trust arbitrary app origins.

`registerNativePasskey(session, relayURL)` enrolls the OS credential.
`getNativeAssertion(session, relayURL, approval.digest)` binds verification
to the computer, phone, action digest and a single-use server nonce. A supplied
`{nonce: qid}` is reserved for a queued action already bound to that queue ID.

The adapter and native screens still require real-device verification of
domain associations, cancellation, Android signing origin and destructive
approval rejection. No deployment domain or signing identity is baked in.

Remote push is explicitly **unavailable** in the native app. The current
relay accepts browser Web Push endpoints/keys and RFC 8291 ciphertext.
Native FCM/APNs/Expo push tokens are different; completing native delivery
requires backend changes and platform credentials. The app does not pretend
that a local notification or Expo token implements this protocol. The
`nativePushCapability` export exposes this limit to callers.

References: [module API and platform setup](https://github.com/peterferguson/react-native-passkeys),
[Android passkey origins](https://developer.android.com/identity/passkeys/create-passkeys).
