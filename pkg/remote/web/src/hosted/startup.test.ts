import { expect, test } from "bun:test";
import { b64urlEncode } from "../b64";
import { chatHash, readPairOffer, routeOf } from "./startup";

test("pairing offer is consumed before stripping its secret", () => {
  const blob = {
    v: 1,
    cid: "pc",
    relay: "https://relay.example",
    s: b64urlEncode(new Uint8Array(32)),
    psk: b64urlEncode(new Uint8Array(32)),
    exp: Math.floor(Date.now() / 1000) + 300,
    name: "Office",
  };
  let stripped = false;
  const offer = readPairOffer(
    `#p=${b64urlEncode(new TextEncoder().encode(JSON.stringify(blob)))}`,
    () => {
      stripped = true;
    },
  );
  expect(offer.blob?.name).toBe("Office");
  expect(stripped).toBe(true);
  expect(offer.error).toBeNull();
});
test("invalid pairing secrets are removed and reported", () => {
  let count = 0;
  expect(
    readPairOffer("#p=invalid", () => {
      count++;
    }).error,
  ).not.toBeNull();
  expect(count).toBe(1);
  expect(
    readPairOffer("#/", () => {
      count++;
    }).blob,
  ).toBeNull();
  expect(count).toBe(1);
});
test("chat routes preserve encoded project and session ids", () => {
  expect(routeOf(chatHash("pc", "project/name", "session one"))).toEqual({
    name: "chat",
    cid: "pc",
    pj: "project/name",
    sessionId: "session one",
  });
  expect(routeOf("#/c/pc/p/%zz/s/a")).toEqual({ name: "computers" });
});
