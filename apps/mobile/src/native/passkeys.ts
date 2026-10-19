import { b64urlDecode, b64urlEncode } from "../../../../pkg/remote/web/src/b64";
import {
  type Assertion,
  decodeAssertChallengeResult,
  decodePasskeyBeginResult,
  decodePasskeyInfo,
  type PasskeyInfo,
} from "../../../../pkg/remote/web/src/protocol3";
import type { SessionLike } from "../../../../pkg/remote/web/src/state3";

type Session = Pick<SessionLike, "call" | "welcome">;
export type NativePasskeyAPI = Pick<
  typeof import("react-native-passkeys"),
  "create" | "get" | "isSupported"
>;

function relyingParty(relayURL: string): string {
  const url = new URL(relayURL);
  if (url.protocol !== "https:" || url.username || url.password) {
    throw new Error("Native passkeys require the relay's verified HTTPS domain.");
  }
  return url.hostname;
}

function available(api: NativePasskeyAPI): void {
  if (!api.isSupported())
    throw new Error(
      "Passkeys are unavailable on this device. Use a supported signed development or release build.",
    );
}

function encoded(value: unknown, label: string): string {
  if (typeof value !== "string") throw new Error(`Missing ${label} from the authenticator.`);
  const raw = b64urlDecode(value);
  if (!raw?.length || b64urlEncode(raw) !== value)
    throw new Error(`Invalid ${label} from the authenticator.`);
  return value;
}

async function keys(session: Session): Promise<PasskeyInfo[]> {
  const result = await session.call("passkey.list");
  if (!Array.isArray(result)) throw new Error("The computer returned an invalid passkey list.");
  return result.map((item) => {
    const key = decodePasskeyInfo(item);
    if (!key) throw new Error("The computer returned an invalid passkey.");
    encoded(key.id, "credential id");
    return key;
  });
}

export async function registerNativePasskey(
  session: Session,
  relayURL: string,
  platform?: NativePasskeyAPI,
): Promise<void> {
  const api = platform ?? (await import("react-native-passkeys"));
  available(api);
  const rpId = relyingParty(relayURL);
  const begin = decodePasskeyBeginResult(await session.call("passkey.begin"));
  if (!begin || begin.rp_id !== rpId)
    throw new Error("The computer's passkey domain does not match this relay.");
  const existing = await keys(session);
  const credential = await api.create({
    challenge: encoded(begin.challenge, "registration challenge"),
    rp: { id: rpId, name: "Deck" },
    user: {
      id: encoded(begin.user_id, "user id"),
      name: session.welcome.device.id,
      displayName: session.welcome.device.name,
    },
    pubKeyCredParams: [
      { type: "public-key", alg: -7 },
      { type: "public-key", alg: -8 },
    ],
    timeout: 120_000,
    attestation: "none",
    authenticatorSelection: { userVerification: "required", residentKey: "preferred" },
    excludeCredentials: existing.map((key) => ({ type: "public-key", id: key.id })),
  });
  if (!credential) throw new Error("Passkey registration was cancelled.");
  if (credential.type !== "public-key")
    throw new Error("The authenticator returned an invalid passkey.");
  // Forward OS clientDataJSON verbatim. In particular, Android's signed-app
  // origin must be explicitly trusted by the computer; never replace it with
  // an HTTPS origin or treat a local biometric result as an assertion.
  await session.call("passkey.finish", {
    credential_id: encoded(credential.rawId, "credential id"),
    client_data_json: encoded(credential.response.clientDataJSON, "client data"),
    attestation_object: encoded(credential.response.attestationObject, "attestation"),
  });
}

export async function getNativeAssertion(
  session: Session,
  relayURL: string,
  digest: string,
  options: { nonce?: string; platform?: NativePasskeyAPI } = {},
): Promise<Assertion> {
  const api = options.platform ?? (await import("react-native-passkeys"));
  available(api);
  const rpId = relyingParty(relayURL);
  const existing = await keys(session);
  if (!existing.length)
    throw new Error("Add a passkey to this phone before approving destructive actions.");
  let nonce = options.nonce;
  if (nonce === undefined) {
    const result = decodeAssertChallengeResult(
      await session.call("assert.challenge", { action_digest: digest }),
    );
    if (!result?.nonce) throw new Error("The computer returned an invalid assertion nonce.");
    nonce = result.nonce;
  }
  if (!nonce || !digest) throw new Error("A bound action digest and nonce are required.");
  const input = new TextEncoder().encode(
    ["deck-assert-v1", session.welcome.server.cid, session.welcome.device.id, digest, nonce].join(
      "\0",
    ),
  );
  const challenge = b64urlEncode(new Uint8Array(await crypto.subtle.digest("SHA-256", input)));
  const credential = await api.get({
    challenge,
    rpId,
    userVerification: "required",
    timeout: 120_000,
    allowCredentials: existing.map((key) => ({ type: "public-key", id: key.id })),
  });
  if (!credential) throw new Error("Passkey verification was cancelled.");
  if (credential.type !== "public-key")
    throw new Error("The authenticator returned an invalid assertion.");
  return {
    credential_id: encoded(credential.rawId, "credential id"),
    authenticator_data: encoded(credential.response.authenticatorData, "authenticator data"),
    client_data_json: encoded(credential.response.clientDataJSON, "client data"),
    signature: encoded(credential.response.signature, "signature"),
  };
}

export const assertNativeAction = getNativeAssertion;
