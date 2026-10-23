import { b64urlEncode } from "../../../../pkg/remote/web/src/b64";
import { parsePairFragment } from "../../../../pkg/remote/web/src/hosted/pairing";
import { decodeQRBlob, type QRBlob } from "../../../../pkg/remote/web/src/protocol3";

// Scanner data and deep links are secret inputs. Errors intentionally never
// include the original URL, JSON or fragment, and callers must not log it.
export function parseNativePairInput(
  input: string,
  nowMs = Date.now(),
  allowLAN = process.env.EXPO_PUBLIC_DECK_LAN_HTTP === "1",
): QRBlob {
  let fragment: string;
  const text = input.trim();
  if (text.startsWith("#p=") || text.startsWith("p=")) fragment = text;
  else if (text.startsWith("{")) {
    try {
      const decoded = decodeQRBlob(JSON.parse(text));
      if (decoded === null) throw new Error();
      fragment = `#p=${b64urlEncode(new TextEncoder().encode(JSON.stringify(decoded)))}`;
    } catch {
      throw new Error("This is not a valid Deck pairing code.");
    }
  } else {
    try {
      const url = new URL(text);
      if (!["https:", "http:", "deck:", "deck-mobile:"].includes(url.protocol)) throw new Error();
      fragment = url.hash;
    } catch {
      throw new Error("This is not a valid Deck pairing link.");
    }
  }
  const blob = parsePairFragment(fragment, nowMs);
  let relay: URL;
  try {
    relay = new URL(blob.relay);
  } catch {
    throw new Error("The pairing code has an invalid relay address.");
  }
  const parts = relay.hostname.split(".").map(Number);
  const privateIPv4 =
    /^\d+\.\d+\.\d+\.\d+$/.test(relay.hostname) &&
    parts.every((part) => part >= 0 && part <= 255) &&
    (parts[0] === 10 ||
      (parts[0] === 172 && (parts[1] ?? -1) >= 16 && (parts[1] ?? -1) <= 31) ||
      (parts[0] === 192 && parts[1] === 168));
  const local = ["localhost", "127.0.0.1", "[::1]"].includes(relay.hostname);
  if (relay.protocol === "http:" && privateIPv4 && !allowLAN)
    throw new Error(
      "This computer uses a local-network relay, and this build of Deck only accepts secure (HTTPS) relays. Install the LAN build of the app, or give the computer an HTTPS relay.",
    );
  if (
    (relay.protocol !== "https:" &&
      !(relay.protocol === "http:" && (local || (allowLAN && privateIPv4)))) ||
    relay.username ||
    relay.password ||
    relay.search ||
    relay.hash ||
    relay.pathname !== "/" ||
    !/^[A-Za-z0-9_-]{1,128}$/.test(blob.cid)
  ) {
    throw new Error("The pairing code must use a secure relay origin.");
  }
  return { ...blob, relay: relay.origin };
}
