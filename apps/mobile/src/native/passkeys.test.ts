import { expect, test } from "bun:test";
import { b64urlEncode } from "../../../../pkg/remote/web/src/b64";
import type { SessionLike } from "../../../../pkg/remote/web/src/state3";
import challengeVectors from "../../../../pkg/remote/wire/testdata/assert_challenge.json";
import { getNativeAssertion, type NativePasskeyAPI, registerNativePasskey } from "./passkeys";

const encode = (value: string) => b64urlEncode(new TextEncoder().encode(value));
const clientData = encode(JSON.stringify({ origin: "android:apk-key-hash:SIGNED_APP_HASH" }));
function fixture() {
  const calls: { method: string; params: unknown }[] = [];
  const session = {
    welcome: { server: { cid: "computer" }, device: { id: "phone", name: "Phone" } },
    call: async (method: string, params?: unknown) => {
      calls.push({ method, params });
      if (method === "passkey.begin")
        return {
          challenge: encode("register-challenge"),
          rp_id: "relay.example",
          user_id: encode("user"),
        };
      if (method === "passkey.list")
        return [{ id: encode("credential"), created_at: "2026-10-15T00:00:00Z" }];
      if (method === "assert.challenge") return { nonce: "one-use-nonce" };
      return {};
    },
  } as unknown as Pick<SessionLike, "call" | "welcome">;
  const api: NativePasskeyAPI = {
    isSupported: () => true,
    create: async () => ({
      type: "public-key",
      id: encode("credential"),
      rawId: encode("credential"),
      clientExtensionResults: {},
      response: {
        clientDataJSON: clientData,
        attestationObject: encode("attestation"),
        getPublicKey: () => null,
      },
    }),
    get: async () => ({
      type: "public-key",
      id: encode("credential"),
      rawId: encode("credential"),
      clientExtensionResults: {},
      response: {
        clientDataJSON: clientData,
        authenticatorData: encode("authenticator"),
        signature: encode("signature"),
      },
    }),
  };
  return { calls, session, api };
}

test("native enrollment preserves OS clientDataJSON and requires verified RP domain", async () => {
  const { calls, session, api } = fixture();
  await registerNativePasskey(session, "https://relay.example", api);
  expect(calls.at(-1)).toEqual({
    method: "passkey.finish",
    params: {
      credential_id: encode("credential"),
      client_data_json: clientData,
      attestation_object: encode("attestation"),
    },
  });
  const wrong = fixture();
  await expect(
    registerNativePasskey(wrong.session, "https://other.example", wrong.api),
  ).rejects.toThrow("does not match");
  expect(wrong.calls.some((call) => call.method === "passkey.finish")).toBe(false);
});
test("cancelled native enrollment never finishes registration", async () => {
  const { calls, session, api } = fixture();
  await expect(
    registerNativePasskey(session, "https://relay.example", { ...api, create: async () => null }),
  ).rejects.toThrow("cancelled");
  expect(calls.some((call) => call.method === "passkey.finish")).toBe(false);
});
test("native assertion binds cid, phone, action digest and server nonce", async () => {
  const { session, api, calls } = fixture();
  let request: Parameters<NativePasskeyAPI["get"]>[0] | undefined;
  const result = await getNativeAssertion(session, "https://relay.example", "action-digest", {
    platform: {
      ...api,
      get: async (value) => {
        request = value;
        return api.get(value);
      },
    },
  });
  const expected = new Uint8Array(
    await crypto.subtle.digest(
      "SHA-256",
      new TextEncoder().encode("deck-assert-v1\0computer\0phone\0action-digest\0one-use-nonce"),
    ),
  );
  expect(request?.challenge).toBe(b64urlEncode(expected));
  expect(request?.userVerification).toBe("required");
  expect(request?.rpId).toBe("relay.example");
  expect(result.client_data_json).toBe(clientData);
  expect(calls.some((call) => call.method === "approvals.answer")).toBe(false);
});
test("unsupported and cancelled authentication cannot produce an assertion", async () => {
  const { session, api } = fixture();
  await expect(
    getNativeAssertion(session, "https://relay.example", "digest", {
      platform: { ...api, isSupported: () => false },
    }),
  ).rejects.toThrow("unavailable");
  await expect(
    getNativeAssertion(session, "https://relay.example", "digest", {
      platform: { ...api, get: async () => null },
    }),
  ).rejects.toThrow("cancelled");
});

test("native challenges match independently generated Go vectors, including queue nonce and Unicode", async () => {
  for (const vector of challengeVectors.cases.filter((item) => item.nonce !== "")) {
    const { session, api, calls } = fixture();
    session.welcome.server.cid = vector.cid;
    session.welcome.device.id = vector.device_id;
    let challenge: string | undefined;
    await getNativeAssertion(session, "https://relay.example", vector.action_digest, {
      nonce: vector.nonce,
      platform: {
        ...api,
        get: async (request) => {
          challenge = request.challenge;
          return api.get(request);
        },
      },
    });
    expect(challenge).toBe(vector.challenge_b64url);
    expect(calls.some((call) => call.method === "assert.challenge")).toBe(false);
  }
});
