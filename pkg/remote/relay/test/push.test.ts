import { describe, expect, test } from "vitest";
import { importVapidPrivate, postPush, signVapid } from "../src/push";

async function exportJwk(key: CryptoKey): Promise<JsonWebKey> {
  const out = await crypto.subtle.exportKey("jwk", key);
  if (out instanceof ArrayBuffer) throw new Error("unexpected export shape");
  return out;
}

function b64uToBytes(s: string): Uint8Array {
  const bin = atob(s.replaceAll("-", "+").replaceAll("_", "/"));
  return Uint8Array.from(bin, (c) => c.charCodeAt(0));
}

describe("vapid", () => {
  test("jwt verifies against the public key", async () => {
    const kp = (await crypto.subtle.generateKey({ name: "ECDSA", namedCurve: "P-256" }, true, [
      "sign",
      "verify",
    ])) as CryptoKeyPair;
    const jwk = await exportJwk(kp.privateKey);
    const pubJwk = await exportJwk(kp.publicKey);
    if (jwk["d"] === undefined || pubJwk["x"] === undefined || pubJwk["y"] === undefined) {
      throw new Error("no jwk");
    }
    const jwkB64u = btoa(
      JSON.stringify({ kty: "EC", crv: "P-256", x: pubJwk["x"], y: pubJwk["y"], d: jwk["d"] }),
    )
      .replaceAll("+", "-")
      .replaceAll("/", "_")
      .replaceAll("=", "");
    const priv = await importVapidPrivate(jwkB64u);
    const jwt = await signVapid(
      { subject: "mailto:test@example.com", privateKey: priv },
      "https://fcm.googleapis.com",
      1893456000,
    );
    const [h, b, s] = jwt.split(".");
    expect([h, b, s].every((x) => x !== undefined && x !== "")).toBe(true);
    expect(JSON.parse(atob(h ?? ""))).toEqual({ typ: "JWT", alg: "ES256" });
    expect(JSON.parse(atob((b ?? "").replaceAll("-", "+").replaceAll("_", "/")))).toEqual({
      aud: "https://fcm.googleapis.com",
      exp: 1893456000,
      sub: "mailto:test@example.com",
    });
    const pub = await crypto.subtle.importKey(
      "jwk",
      { kty: "EC", crv: "P-256", x: pubJwk["x"], y: pubJwk["y"] },
      { name: "ECDSA", namedCurve: "P-256" },
      false,
      ["verify"],
    );
    const ok = await crypto.subtle.verify(
      { name: "ECDSA", hash: "SHA-256" },
      pub,
      b64uToBytes(s ?? ""),
      new TextEncoder().encode(`${h}.${b}`),
    );
    expect(ok).toBe(true);
  });

  test("postPush sends vapid headers and the ciphertext", async () => {
    const kp = (await crypto.subtle.generateKey({ name: "ECDSA", namedCurve: "P-256" }, true, [
      "sign",
      "verify",
    ])) as CryptoKeyPair;
    const jwk = await exportJwk(kp.privateKey);
    const pubJwk = await exportJwk(kp.publicKey);
    if (jwk["d"] === undefined || pubJwk["x"] === undefined || pubJwk["y"] === undefined) {
      throw new Error("no jwk");
    }
    const seen: { url: string; init: RequestInit }[] = [];
    const stub = async (url: string, init: RequestInit): Promise<Response> => {
      seen.push({ url, init });
      return new Response("ok", { status: 201 });
    };
    const payload = new TextEncoder().encode("ciphertext-bytes");
    const jwkB64u = btoa(
      JSON.stringify({ kty: "EC", crv: "P-256", x: pubJwk["x"], y: pubJwk["y"], d: jwk["d"] }),
    )
      .replaceAll("+", "-")
      .replaceAll("/", "_")
      .replaceAll("=", "");
    const res = await postPush(
      {
        subject: "mailto:test@example.com",
        privateKey: await importVapidPrivate(jwkB64u),
        publicB64url: `${pubJwk["x"]}.${pubJwk["y"]}`,
      },
      { endpoint: "https://push.example.com/sub/abc", payload, ttl: 3600, urgency: "high" },
      stub as typeof fetch,
    );
    expect(res).toEqual({ ok: true, status: 201 });
    expect(seen).toHaveLength(1);
    const call = seen[0];
    if (call === undefined) throw new Error("no call");
    expect(call.url).toBe("https://push.example.com/sub/abc");
    const headers = new Headers(call.init.headers);
    expect(headers.get("content-encoding")).toBe("aes128gcm");
    expect(headers.get("ttl")).toBe("3600");
    expect(headers.get("urgency")).toBe("high");
    expect(headers.get("authorization") ?? "").toMatch(
      /^vapid t=[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+, k=.+$/,
    );
    expect(new Uint8Array(call.init.body as ArrayBuffer)).toEqual(payload);
  });
});
