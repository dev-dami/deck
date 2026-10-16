// Relay/v1 phone endpoints (docs/mobile-contract.md §5).
import { type OpenOptions, openSocket, type Transport } from "./transport";

function wsBase(relayUrl: string): string {
  const u = new URL(relayUrl);
  if (u.protocol === "https:") u.protocol = "wss:";
  else if (u.protocol === "http:") u.protocol = "ws:";
  else throw new Error("the relay address must be http or https");
  return u.origin;
}

// A normal session: the first text frame is the relay token, never the URL.
export function openRelayDevice(
  relayUrl: string,
  cid: string,
  token: string,
  opts: OpenOptions = {},
): Promise<Transport> {
  const url = `${wsBase(relayUrl)}/r/v1/c/${encodeURIComponent(cid)}/dev`;
  return openSocket(url, { ...opts, firstFrame: JSON.stringify({ token }) });
}

// The pairing handshake: the first text frame names the pairing window.
export function openRelayPair(
  relayUrl: string,
  cid: string,
  pairId: string,
  opts: OpenOptions = {},
): Promise<Transport> {
  const url = `${wsBase(relayUrl)}/r/v1/c/${encodeURIComponent(cid)}/pair`;
  return openSocket(url, { ...opts, firstFrame: JSON.stringify({ pair_id: pairId }) });
}
