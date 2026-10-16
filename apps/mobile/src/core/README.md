# Native protocol and persistence

`NativeClient.bootstrap()` opens the native client after the entry point installs
QuickCrypto and its `CryptoKey` constructor. It checks the installed provider
against the Go IK, IKpsk2 and K handshake/transport vectors and the Go pairing
HKDF vectors before touching device identities or connecting. Unsupported or
incompatible encryption prevents startup. There is no JavaScript crypto fallback.

The installed QuickCrypto 1.1.8 source implements X25519 JWK/raw imports and
derivation, AES-GCM, SHA-256, HMAC and HKDF in its native provider. Host tests run
the verifier on Bun's native WebCrypto; they cannot load React Native's C++/JSI
bridge. The app repeats the vector check on its actual native provider at startup.

The facade reuses the browser-independent Noise, Session3, FleetState, ChatSession,
queue and snapshot protocol implementations. Its small shared seams are a storage
backend injection and an externally supplied pairing identity. Event subscriptions
return cleanup functions; `ChatSession.detach()` releases its subscription.

SQLite stores public computer metadata, already sealed outbox messages, bounded
delivery metadata and AES-GCM encrypted snapshot rows. An OS-backed SecureStore
item holds the phone's raw32 identity, relay token, device ID, pinned computer key,
relay origin and random snapshot key. Items use
`WHEN_UNLOCKED_THIS_DEVICE_ONLY`. Reconstructing an identity imports a
non-extractable native key and wipes temporary byte buffers. Missing or inaccessible
SecureStore data never falls back to an unprotected identity.

Every snapshot write uses a fresh random 96-bit IV. Authenticated data binds the
cache version, store and computer ID, so ciphertext cannot be moved between rows.
Deleting the secure identity makes its snapshot cache unreadable. The public peer
pin and relay origin are restored from SecureStore as well, preventing edits to
SQLite metadata from redirecting the relay token.

`queue()`, `flush()` and `cancel()` serialize work per computer. A cancellation
waits for any active flush, then removes an unsent action or sends a sealed
`queue.cancel` if delivery already completed. A mailbox acknowledgement is delivery
only; the computer's receipt is the execution outcome.

Run `bun test src/core src/storage src/crypto` from `apps/mobile` for real Noise
pairing/restart tests, persistence boundaries, cache integrity and queue races.
Native JSI, platform Keychain/Keystore and OS lifecycle behavior still require a
signed development or release build on a device.

References: [QuickCrypto coverage](https://github.com/margelo/react-native-quick-crypto/blob/main/docs/data/coverage.ts),
[Expo SecureStore](https://docs.expo.dev/versions/latest/sdk/securestore/),
[Expo SQLite](https://docs.expo.dev/versions/latest/sdk/sqlite/).
