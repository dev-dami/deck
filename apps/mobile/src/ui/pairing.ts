import { parseNativePairInput } from "../core/pair-input";

export function previewPairing(
  link: string,
  now = Date.now(),
): { name: string; relay: string; localHTTP: boolean } {
  const trimmed = link.trim();
  if (!trimmed || trimmed.length > 16_384) throw new Error("Enter a valid Deck pairing link.");
  const blob = parseNativePairInput(trimmed, now);
  const relay = new URL(blob.relay);
  return { name: blob.name, relay: relay.host, localHTTP: relay.protocol === "http:" };
}
