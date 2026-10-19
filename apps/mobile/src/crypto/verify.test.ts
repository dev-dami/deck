import { expect, test } from "bun:test";
import { verifyNativeCrypto } from "./verify";

test("native startup verifier reproduces all Go Noise and HKDF vectors on host native WebCrypto", async () => {
  // Bun executes the real host native provider, without mocked primitives.
  // The app repeats this check against QuickCrypto's C++/JSI on each boot.
  await expect(verifyNativeCrypto()).resolves.toBeUndefined();
});

test("an unavailable algorithm fails closed without exposing provider inputs", async () => {
  const original = globalThis.crypto.subtle.importKey;
  globalThis.crypto.subtle.importKey = (() => {
    throw new Error("Provider failed for PRIVATE-KEY-INPUT");
  }) as typeof original;
  try {
    await expect(verifyNativeCrypto()).rejects.toThrow("Native encryption is unavailable");
    try {
      await verifyNativeCrypto();
    } catch (error) {
      expect(String(error)).not.toContain("PRIVATE-KEY-INPUT");
    }
  } finally {
    globalThis.crypto.subtle.importKey = original;
  }
});
