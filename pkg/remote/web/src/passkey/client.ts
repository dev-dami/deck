import { b64urlDecode, b64urlEncode } from "../b64";
import {
  type AnswerDecision,
  type ApprovalV3,
  type Assertion,
  decodeAssertChallengeResult,
  decodePasskeyBeginResult,
  decodePasskeyInfo,
  type PasskeyInfo,
} from "../protocol3";
import type { SessionLike } from "../state3";

type Rpc = Pick<SessionLike, "call" | "welcome">;

// Injectable browser boundary; tests can exercise cancellation and malformed
// authenticator replies without substituting any assertion in production.
export interface PasskeyBrowser {
  hostname: string;
  create(options: CredentialCreationOptions): Promise<Credential | null>;
  get(options: CredentialRequestOptions): Promise<Credential | null>;
}

function browser(): PasskeyBrowser {
  if (!globalThis.isSecureContext || !navigator.credentials || !globalThis.PublicKeyCredential) {
    throw new Error("Passkeys require a supported browser and HTTPS.");
  }
  return {
    hostname: location.hostname,
    create: (options) => navigator.credentials.create(options),
    get: (options) => navigator.credentials.get(options),
  };
}

function bytes(value: string, label: string): Uint8Array<ArrayBuffer> {
  const decoded = b64urlDecode(value);
  if (decoded === null || decoded.length === 0) throw new Error(`Invalid ${label}.`);
  return new Uint8Array(decoded);
}

function encode(value: ArrayBuffer, label: string): string {
  if (!(value instanceof ArrayBuffer) || value.byteLength === 0) {
    throw new Error(`The authenticator returned no ${label}.`);
  }
  return b64urlEncode(new Uint8Array(value));
}

export async function assertionChallenge(
  cid: string,
  deviceId: string,
  digest: string,
  nonce: string,
): Promise<Uint8Array<ArrayBuffer>> {
  const input = new TextEncoder().encode(
    ["deck-assert-v1", cid, deviceId, digest, nonce].join("\0"),
  );
  return new Uint8Array(await crypto.subtle.digest("SHA-256", input));
}

export async function yoloDigest(pj: string, sessionId: string): Promise<string> {
  const digest = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(`mode.set|yolo|${pj}|${sessionId}`),
  );
  return Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
}

export async function listPasskeys(session: Rpc): Promise<PasskeyInfo[]> {
  const result = await session.call("passkey.list");
  if (!Array.isArray(result)) throw new Error("The computer sent an invalid passkey list.");
  return result.map((item) => {
    const decoded = decodePasskeyInfo(item);
    if (decoded === null) throw new Error("The computer sent an invalid passkey.");
    return decoded;
  });
}

export async function registerPasskey(session: Rpc, platform?: PasskeyBrowser): Promise<void> {
  const api = platform ?? browser();
  const begin = decodePasskeyBeginResult(await session.call("passkey.begin"));
  if (begin === null || begin.rp_id !== api.hostname) {
    throw new Error("The computer's passkey domain does not match this app.");
  }
  const existing = await listPasskeys(session);
  const credential = (await api.create({
    publicKey: {
      challenge: bytes(begin.challenge, "registration challenge"),
      rp: { id: begin.rp_id, name: "Deck" },
      user: {
        id: bytes(begin.user_id, "user id"),
        name: session.welcome.device.id,
        displayName: session.welcome.device.name,
      },
      pubKeyCredParams: [
        { type: "public-key", alg: -7 },
        { type: "public-key", alg: -8 },
      ],
      authenticatorSelection: { userVerification: "required", residentKey: "preferred" },
      attestation: "none",
      timeout: 120_000,
      excludeCredentials: existing.map((key) => ({
        type: "public-key",
        id: bytes(key.id, "credential id"),
      })),
    },
  })) as PublicKeyCredential | null;
  if (credential === null) throw new Error("Passkey registration was cancelled.");
  if (credential.type !== "public-key" || !credential.response) {
    throw new Error("The authenticator returned an invalid passkey.");
  }
  const response = credential.response as AuthenticatorAttestationResponse;
  await session.call("passkey.finish", {
    credential_id: encode(credential.rawId, "credential id"),
    client_data_json: encode(response.clientDataJSON, "client data"),
    attestation_object: encode(response.attestationObject, "attestation"),
  });
}

export async function removePasskey(session: Rpc, id: string): Promise<void> {
  await session.call("passkey.remove", { id });
}

export async function getAssertion(
  session: Rpc,
  digest: string,
  options: { nonce?: string; platform?: PasskeyBrowser } = {},
): Promise<Assertion> {
  const api = options.platform ?? browser();
  const keys = await listPasskeys(session);
  if (keys.length === 0) throw new Error("Add a passkey before approving this action.");
  let nonce = options.nonce;
  if (nonce === undefined) {
    const result = decodeAssertChallengeResult(
      await session.call("assert.challenge", { action_digest: digest }),
    );
    if (result === null || result.nonce === "") {
      throw new Error("The computer sent an invalid assertion challenge.");
    }
    nonce = result.nonce;
  }
  if (nonce === "") throw new Error("An assertion nonce is required.");
  const challenge = await assertionChallenge(
    session.welcome.server.cid,
    session.welcome.device.id,
    digest,
    nonce,
  );
  const credential = (await api.get({
    publicKey: {
      challenge,
      rpId: api.hostname,
      userVerification: "required",
      timeout: 120_000,
      allowCredentials: keys.map((key) => ({
        type: "public-key",
        id: bytes(key.id, "credential id"),
      })),
    },
  })) as PublicKeyCredential | null;
  if (credential === null) throw new Error("Passkey verification was cancelled.");
  if (credential.type !== "public-key" || !credential.response) {
    throw new Error("The authenticator returned an invalid assertion.");
  }
  const response = credential.response as AuthenticatorAssertionResponse;
  return {
    credential_id: encode(credential.rawId, "credential id"),
    authenticator_data: encode(response.authenticatorData, "authenticator data"),
    client_data_json: encode(response.clientDataJSON, "client data"),
    signature: encode(response.signature, "signature"),
  };
}

export async function answerApproval(
  session: Rpc,
  pj: string,
  approval: ApprovalV3,
  decision: AnswerDecision,
  reason = "",
  platform?: PasskeyBrowser,
): Promise<void> {
  const params: Record<string, unknown> = {
    session_id: approval.session_id,
    id: approval.id,
    decision,
  };
  if (decision === "deny" && reason.trim()) params["reason"] = reason.trim();
  if (decision !== "deny" && approval.tier !== "normal") {
    params["assertion"] = await getAssertion(
      session,
      approval.digest,
      platform === undefined ? {} : { platform },
    );
  }
  await session.call("approvals.answer", params, pj);
}

export async function enableYolo(
  session: Rpc,
  pj: string,
  sessionId: string,
  platform?: PasskeyBrowser,
): Promise<void> {
  if (!session.welcome.server.allow_yolo) {
    throw new Error("This phone does not have permission to enable yolo.");
  }
  const assertion = await getAssertion(
    session,
    await yoloDigest(pj, sessionId),
    platform === undefined ? {} : { platform },
  );
  await session.call("mode.set", { session_id: sessionId, mode: "yolo", assertion }, pj);
}
