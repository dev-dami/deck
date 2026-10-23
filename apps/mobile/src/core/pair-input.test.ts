import { expect, test } from "bun:test";
import { b64urlEncode } from "../../../../pkg/remote/web/src/b64";
import { parseNativePairInput } from "./pair-input";

function code(overrides: Record<string, unknown> = {}) {
  const blob = {
    v: 1,
    cid: "computer_1",
    relay: "https://relay.example",
    s: b64urlEncode(new Uint8Array(32).fill(4)),
    psk: b64urlEncode(new Uint8Array(32).fill(8)),
    exp: 1_900_000_000,
    name: "Office",
    ...overrides,
  };
  const fragment = `#p=${b64urlEncode(new TextEncoder().encode(JSON.stringify(blob)))}`;
  return { blob, fragment };
}

test("URL, fragment, deep link and JSON scanner inputs share QR validation", () => {
  const { blob, fragment } = code();
  for (const text of [
    fragment,
    `https://app.example/${fragment}`,
    `deck://pair${fragment}`,
    `deck-mobile://pair${fragment}`,
    JSON.stringify(blob),
  ]) {
    expect(parseNativePairInput(text, 1_800_000_000_000)).toEqual(blob);
  }
});

test("expired QR codes are refused before network or private-key generation", () => {
  expect(() => parseNativePairInput(code({ exp: 1 }).fragment)).toThrow("expired");
});

for (const relay of [
  "http://relay.example",
  "https://user:secret@relay.example",
  "https://relay.example/path",
  "https://relay.example?token=secret",
]) {
  test(`unsafe relay origin is refused (${new URL(relay).hostname})`, () => {
    expect(() => parseNativePairInput(code({ relay }).fragment, 1_800_000_000_000)).toThrow(
      "secure relay origin",
    );
  });
}

test("loopback HTTP is available for local interoperability tests", () => {
  const { fragment } = code({ relay: "http://127.0.0.1:8787" });
  expect(parseNativePairInput(fragment, 1_800_000_000_000).relay).toBe("http://127.0.0.1:8787");
});

test("invalid-link errors never echo QR secrets", () => {
  const input = "not a URL with SECRET-PSK";
  try {
    parseNativePairInput(input);
    throw new Error("expected invalid input");
  } catch (error) {
    expect(String(error)).not.toContain("SECRET-PSK");
  }
});

for (const host of ["10.0.0.2", "172.16.0.2", "172.31.255.254", "192.168.1.2"]) {
  test(`LAN HTTP requires explicit opt-in (${host})`, () => {
    const { fragment } = code({ relay: `http://${host}:8787` });
    expect(() => parseNativePairInput(fragment, 1_800_000_000_000, false)).toThrow(
      "local-network relay",
    );
    expect(parseNativePairInput(fragment, 1_800_000_000_000, true).relay).toBe(
      `http://${host}:8787`,
    );
  });
}

for (const host of ["8.8.8.8", "172.15.0.1", "172.32.0.1", "169.254.1.2", "relay.example"]) {
  test(`LAN profile still refuses public or unsupported HTTP (${host})`, () => {
    expect(() =>
      parseNativePairInput(
        code({ relay: `http://${host}:8787` }).fragment,
        1_800_000_000_000,
        true,
      ),
    ).toThrow();
  });
}
