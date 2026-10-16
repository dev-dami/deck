import { expect, test } from "bun:test";
import { b64urlEncode } from "../../../../pkg/remote/web/src/b64";
import { previewPairing } from "./pairing";

function link(relay = "https://deck.example", exp = 1000): string {
  const blob = {
    v: 1,
    cid: "computer",
    relay,
    name: "My workstation",
    psk: b64urlEncode(new Uint8Array(32)),
    s: b64urlEncode(new Uint8Array(32)),
    exp,
  };
  return `https://deck.example/#p=${b64urlEncode(new TextEncoder().encode(JSON.stringify(blob)))}`;
}

test("pairing preview reads computer name and relay without pairing", () => {
  expect(previewPairing(link(), 100)).toEqual({
    name: "My workstation",
    relay: "deck.example",
    localHTTP: false,
  });
});
test("pairing preview rejects expired, malformed and insecure codes", () => {
  expect(() => previewPairing(link(), 1_000_000)).toThrow("expired");
  expect(() => previewPairing("not a url")).toThrow();
  expect(() => previewPairing(link("http://relay.example"), 100)).toThrow("secure");
  expect(() => previewPairing(link("https://user:pass@relay.example"), 100)).toThrow("secure");
});
test("native deep links receive the same preview validation as camera URLs", () => {
  expect(
    previewPairing(link().replace("https://deck.example/", "deck-mobile://pair"), 100),
  ).toEqual({ name: "My workstation", relay: "deck.example", localHTTP: false });
});
