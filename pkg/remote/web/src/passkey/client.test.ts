import { describe, expect, test } from "bun:test";
import vector from "../../../wire/testdata/assert_challenge.json";
import welcomeFixture from "../../../wire/testdata/v3_frame_welcome.json";
import { b64urlEncode } from "../b64";
import { type ApprovalV3, decodeWelcome3Frame } from "../protocol3";
import type { SessionLike } from "../state3";
import {
  answerApproval,
  assertionChallenge,
  enableYolo,
  getAssertion,
  type PasskeyBrowser,
  registerPasskey,
  yoloDigest,
} from "./client";

function fake() {
  const welcome = decodeWelcome3Frame(welcomeFixture);
  if (welcome === null) throw new Error("bad fixture");
  const calls: Array<{ method: string; params: unknown; pj: string | undefined }> = [];
  const responses: Record<string, unknown> = {
    "passkey.list": [{ id: "AQID", created_at: "2026-10-14T00:00:00Z" }],
    "assert.challenge": { nonce: "fresh-nonce" },
    "passkey.begin": { challenge: "AQID", rp_id: "app.deck.test", user_id: "BAUG" },
  };
  const session: Pick<SessionLike, "call" | "welcome"> = {
    welcome,
    async call<T>(method: string, params?: unknown, pj?: string): Promise<T> {
      calls.push({ method, params, pj });
      return responses[method] as T;
    },
  };
  let getOptions: CredentialRequestOptions | undefined;
  let createOptions: CredentialCreationOptions | undefined;
  const buffer = (n: number): ArrayBuffer => new Uint8Array([n]).buffer;
  let result: Credential | null = {
    type: "public-key",
    rawId: buffer(1),
    response: {
      clientDataJSON: buffer(2),
      authenticatorData: buffer(3),
      signature: buffer(4),
      attestationObject: buffer(5),
    },
  } as unknown as Credential;
  const platform: PasskeyBrowser = {
    hostname: "app.deck.test",
    async get(options) {
      getOptions = options;
      return result;
    },
    async create(options) {
      createOptions = options;
      return result;
    },
  };
  return {
    session,
    platform,
    responses,
    calls,
    getOptions: () => getOptions,
    createOptions: () => createOptions,
    result: (value: Credential | null) => {
      result = value;
    },
  };
}

const approval: ApprovalV3 = {
  id: "apr_1",
  session_id: "ses_1",
  agent: "claude",
  kind: "command",
  description: "Remove files",
  command: "rm -rf build",
  created_at: "2026-10-14T00:00:00Z",
  tier: "destructive",
  digest: "abcdef",
};

describe("passkey challenge contract", () => {
  for (const row of vector.cases) {
    test(`Go vector ${row.device_id} / ${row.nonce}`, async () => {
      const challenge = await assertionChallenge(
        row.cid,
        row.device_id,
        row.action_digest,
        row.nonce,
      );
      expect(b64urlEncode(challenge)).toBe(row.challenge_b64url);
      expect(Buffer.from(challenge).toString("hex")).toBe(row.challenge_hex);
    });
  }
});

test("registration uses required verification and sends real authenticator fields", async () => {
  const f = fake();
  await registerPasskey(f.session, f.platform);
  const options = f.createOptions()?.publicKey;
  expect(options?.rp.id).toBe("app.deck.test");
  expect(options?.authenticatorSelection?.userVerification).toBe("required");
  expect(options?.attestation).toBe("none");
  expect(options?.pubKeyCredParams.map((v) => v.alg)).toEqual([-7, -8]);
  expect(f.calls.at(-1)).toEqual({
    method: "passkey.finish",
    params: { credential_id: "AQ", client_data_json: "Ag", attestation_object: "BQ" },
    pj: undefined,
  });
});

test("registration rejects a different relying party before invoking browser", async () => {
  const f = fake();
  f.responses["passkey.begin"] = { challenge: "AQID", rp_id: "other.test", user_id: "BAUG" };
  await expect(registerPasskey(f.session, f.platform)).rejects.toThrow("domain");
  expect(f.createOptions()).toBeUndefined();
  expect(f.calls.some((v) => v.method === "passkey.finish")).toBe(false);
});

test("cancelled registration never finishes", async () => {
  const f = fake();
  f.result(null);
  await expect(registerPasskey(f.session, f.platform)).rejects.toThrow("cancelled");
  expect(f.calls.some((v) => v.method === "passkey.finish")).toBe(false);
});

test("assertion binds welcome identity, action digest and fresh nonce with required UV", async () => {
  const f = fake();
  await answerApproval(f.session, "pj_1", approval, "allow", "", f.platform);
  const options = f.getOptions()?.publicKey;
  expect(options?.userVerification).toBe("required");
  expect(options?.rpId).toBe("app.deck.test");
  expect(options?.allowCredentials?.length).toBe(1);
  expect(options?.challenge).toEqual(
    await assertionChallenge(
      f.session.welcome.server.cid,
      f.session.welcome.device.id,
      approval.digest,
      "fresh-nonce",
    ),
  );
  expect(f.calls.at(-1)).toEqual({
    method: "approvals.answer",
    params: {
      session_id: "ses_1",
      id: "apr_1",
      decision: "allow",
      assertion: {
        credential_id: "AQ",
        client_data_json: "Ag",
        authenticator_data: "Aw",
        signature: "BA",
      },
    },
    pj: "pj_1",
  });
});

test("destructive allow_session cancellation never answers", async () => {
  const f = fake();
  f.result(null);
  await expect(
    answerApproval(f.session, "pj_1", approval, "allow_session", "", f.platform),
  ).rejects.toThrow("cancelled");
  expect(f.calls.some((v) => v.method === "approvals.answer")).toBe(false);
});

test("missing signature never answers a destructive approval", async () => {
  const f = fake();
  f.result({
    type: "public-key",
    rawId: new Uint8Array([1]).buffer,
    response: {},
  } as unknown as Credential);
  await expect(
    answerApproval(f.session, "pj_1", approval, "allow", "", f.platform),
  ).rejects.toThrow();
  expect(f.calls.some((v) => v.method === "approvals.answer")).toBe(false);
});

test("deny with reason and normal allow do not invoke an authenticator", async () => {
  const f = fake();
  await answerApproval(f.session, "pj_1", approval, "deny", "  Keep files.  ", f.platform);
  await answerApproval(f.session, "pj_1", { ...approval, tier: "normal" }, "allow", "", f.platform);
  expect(f.calls).toEqual([
    {
      method: "approvals.answer",
      params: { id: "apr_1", session_id: "ses_1", decision: "deny", reason: "Keep files." },
      pj: "pj_1",
    },
    {
      method: "approvals.answer",
      params: { id: "apr_1", session_id: "ses_1", decision: "allow" },
      pj: "pj_1",
    },
  ]);
  expect(f.getOptions()).toBeUndefined();
});

test("unregistered passkeys fail closed before asking for a nonce", async () => {
  const f = fake();
  f.responses["passkey.list"] = [];
  await expect(getAssertion(f.session, approval.digest, { platform: f.platform })).rejects.toThrow(
    "Add a passkey",
  );
  expect(f.calls.map((v) => v.method)).toEqual(["passkey.list"]);
});

test("queued assertions use qid without asking for a live nonce", async () => {
  const f = fake();
  await getAssertion(f.session, approval.digest, { platform: f.platform, nonce: "queue-id" });
  expect(f.calls.map((v) => v.method)).toEqual(["passkey.list"]);
  expect(f.getOptions()?.publicKey?.challenge).toEqual(
    await assertionChallenge(
      f.session.welcome.server.cid,
      f.session.welcome.device.id,
      approval.digest,
      "queue-id",
    ),
  );
});

test("yolo refuses a phone without a grant before authenticator interaction", async () => {
  const f = fake();
  f.session.welcome.server.allow_yolo = false;
  await expect(enableYolo(f.session, "pj_1", "ses_1", f.platform)).rejects.toThrow("permission");
  expect(f.calls).toEqual([]);
});

test("yolo uses the specified action digest and a fresh assertion", async () => {
  const f = fake();
  f.session.welcome.server.allow_yolo = true;
  await enableYolo(f.session, "pj_1", "ses_1", f.platform);
  expect(f.calls.find((v) => v.method === "assert.challenge")?.params).toEqual({
    action_digest: await yoloDigest("pj_1", "ses_1"),
  });
  const last = f.calls.at(-1);
  expect(last?.method).toBe("mode.set");
  expect(last?.pj).toBe("pj_1");
  expect(last?.params).toMatchObject({
    session_id: "ses_1",
    mode: "yolo",
    assertion: { signature: "BA" },
  });
});
