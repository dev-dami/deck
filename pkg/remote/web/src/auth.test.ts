import { describe, expect, test } from "bun:test";
import {
  clearToken,
  deviceName,
  fingerprintHex,
  inviteOrigin,
  type KV,
  loadToken,
  offerFromInput,
  pair,
  pairOfferFromHash,
  parseInvite,
  parseRetryAfter,
  saveToken,
  wsUrl,
} from "./auth";

function memory(): KV & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    data,
    get length() {
      return data.size;
    },
    key: (i) => [...data.keys()][i] ?? null,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => {
      data.set(k, v);
    },
    removeItem: (k) => {
      data.delete(k);
    },
  };
}

const FP = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
const INVITE = `deck://pair?v=1&host=203.0.113.5&port=7733&code=ABCDE-23456&fp=${encodeURIComponent(FP)}&name=vps&exp=1760000000`;

describe("token store", () => {
  test("is keyed by server_id with a pointer to the current install", () => {
    const s = memory();
    expect(loadToken(s)).toBeNull();
    expect(saveToken(s, "srv_a", "tok_a")).toBe(true);
    expect(loadToken(s)).toEqual({ serverId: "srv_a", token: "tok_a" });
    expect(s.data.get("deck.remote.token.srv_a")).toBe("tok_a");
    s.data.set("unrelated", "x");
    saveToken(s, "srv_b", "tok_b");
    expect(loadToken(s)).toEqual({ serverId: "srv_b", token: "tok_b" });
    // One origin serves one install: the other install's token is removed,
    // so a key no longer in use cannot linger. Other keys are left alone.
    expect(s.data.has("deck.remote.token.srv_a")).toBe(false);
    expect(s.data.get("unrelated")).toBe("x");
    clearToken(s, "srv_b");
    expect(loadToken(s)).toBeNull();
  });

  test("blocked storage never throws", () => {
    const blocked: KV = {
      get length(): number {
        throw new Error("blocked");
      },
      key: () => {
        throw new Error("blocked");
      },
      getItem: () => {
        throw new Error("blocked");
      },
      setItem: () => {
        throw new Error("blocked");
      },
      removeItem: () => {
        throw new Error("blocked");
      },
    };
    expect(loadToken(blocked)).toBeNull();
    expect(saveToken(blocked, "s", "t")).toBe(false);
    clearToken(blocked, "s");
    expect(loadToken(null)).toBeNull();
  });
});

describe("QR fragment", () => {
  test("reads the code and the fingerprint", () => {
    expect(pairOfferFromHash(`#pair=7H3K-9QWX&fp=${encodeURIComponent(FP)}`)).toEqual({
      code: "7H3K-9QWX",
      fingerprint: FP,
      host: null,
      port: null,
      name: null,
      expires: null,
    });
    expect(pairOfferFromHash("#pair=7H3K-9QWX")?.fingerprint).toBeNull();
    expect(pairOfferFromHash("#/s/x")).toBeNull();
    expect(pairOfferFromHash("#pair=")).toBeNull();
    expect(pairOfferFromHash("#pair=a%00b")).toBeNull();
  });

  test("a present but malformed fingerprint invalidates the offer (R9)", () => {
    // Never a fall-through to the no-fingerprint trust gate.
    expect(pairOfferFromHash("#pair=7H3K-9QWX&fp=SHA256:short")).toBeNull();
    expect(pairOfferFromHash(`#pair=7H3K-9QWX&fp=${encodeURIComponent(FP)}%E2%80%AE`)).toBeNull();
    expect(pairOfferFromHash(`#pair=${"A".repeat(65)}`)).toBeNull();
    expect(pairOfferFromHash("#pair=7H3K%E2%80%A89QWX")).toBeNull();
  });
});

describe("invite line", () => {
  test("parses every field", () => {
    expect(parseInvite(INVITE)).toEqual({
      code: "ABCDE-23456",
      fingerprint: FP,
      host: "203.0.113.5",
      port: "7733",
      name: "vps",
      expires: 1760000000,
    });
    expect(parseInvite(`  ${INVITE}\n`)).not.toBeNull();
    expect(parseInvite(INVITE.replace("deck://", "DECK://"))).not.toBeNull();
  });

  test("name and exp are optional; alt may repeat", () => {
    const min = `deck://pair?v=1&host=my-box.local&port=1&code=X&fp=${encodeURIComponent(FP)}`;
    expect(parseInvite(min)).toMatchObject({ host: "my-box.local", name: null, expires: null });
    expect(parseInvite(`${min}&alt=10.0.0.2&alt=fe80::1`)).not.toBeNull();
  });

  test("rejects what peer.ParseInvite rejects", () => {
    const bad = [
      INVITE.replace("v=1", "v=2"),
      INVITE.replace("v=1&", ""),
      INVITE.replace("host=203.0.113.5", "host=0.0.0.0"),
      INVITE.replace("host=203.0.113.5", "host=bad host"),
      INVITE.replace("port=7733", "port=0"),
      INVITE.replace("port=7733", "port=70000"),
      INVITE.replace("port=7733", "port=07733"),
      INVITE.replace("code=ABCDE-23456", "code="),
      INVITE.replace(encodeURIComponent(FP), "SHA256:short"),
      INVITE.replace("exp=1760000000", "exp=-1"),
      `${INVITE}&extra=1`,
      `${INVITE}&code=AGAIN`,
      `${INVITE}&alt=a&alt=b&alt=c&alt=d`,
      `${INVITE}​`,
      "deck://pair",
      "https://example.com/#pair=x",
      `deck://pair?${"x".repeat(1100)}`,
    ];
    for (const s of bad) expect(parseInvite(s)).toBeNull();
  });

  test("pasted input is an invite or a bare code", () => {
    expect(offerFromInput(INVITE)?.host).toBe("203.0.113.5");
    expect(offerFromInput(" 7H3K-9QWX ")).toMatchObject({ code: "7H3K-9QWX", fingerprint: null });
    expect(offerFromInput("")).toBeNull();
    expect(offerFromInput("deck://nope")).toBeNull();
    expect(offerFromInput("a\u0000b")).toBeNull();
  });

  test("an invite for another host points at that host's page", () => {
    const offer = parseInvite(INVITE);
    if (offer === null) throw new Error("invite");
    expect(
      inviteOrigin(offer, { protocol: "https:", hostname: "203.0.113.5", port: "7733" }),
    ).toEqual({ matches: true, url: null });
    const other = inviteOrigin(offer, { protocol: "https:", hostname: "localhost", port: "7733" });
    expect(other.matches).toBe(false);
    expect(other.url).toBe(
      `https://203.0.113.5:7733/#pair=ABCDE-23456&fp=${encodeURIComponent(FP)}`,
    );
    const bare = offerFromInput("CODE");
    if (bare === null) throw new Error("code");
    expect(inviteOrigin(bare, { protocol: "http:", hostname: "127.0.0.1", port: "" }).matches).toBe(
      true,
    );
  });

  test("an IPv6 invite matches the bracketed page it is on", () => {
    const offer = parseInvite(
      `deck://pair?v=1&host=fd00%3A%3A5&port=7733&code=ABCDE-23456&fp=${encodeURIComponent(FP)}`,
    );
    if (offer === null) throw new Error("invite");
    expect(
      inviteOrigin(offer, { protocol: "https:", hostname: "[fd00::5]", port: "7733" }),
    ).toEqual({ matches: true, url: null });
    expect(
      inviteOrigin(offer, { protocol: "https:", hostname: "[fd00::6]", port: "7733" }).matches,
    ).toBe(false);
  });
});

describe("fingerprint", () => {
  test("renders as colon-separated hex like a certificate viewer", () => {
    const hex = fingerprintHex(FP);
    expect(hex).toBe(Array(32).fill("00").join(":"));
    const real = "SHA256:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU";
    expect(fingerprintHex(real)?.startsWith("E3:B0:C4:42:98:FC:1C:14")).toBe(true);
    expect(fingerprintHex("SHA256:nope")).toBeNull();
    expect(fingerprintHex("AB:CD")).toBeNull();
  });
});

describe("pair()", () => {
  const ok = { token: "t", device_id: "d", server_id: "srv", expires_at: "2027-01-10T09:30:00Z" };

  test("posts JSON same-origin without credentials and decodes the response", async () => {
    const calls: Array<[string, RequestInit]> = [];
    const r = await pair(
      async (url, init) => {
        calls.push([url, init]);
        return Response.json(ok);
      },
      "7H3K-9QWX",
      "Pixel 9",
    );
    expect(r).toEqual({ ok: true, response: ok });
    const [url, init] = calls[0] ?? ["", {}];
    expect(url).toBe("/api/v1/pair");
    expect(init.method).toBe("POST");
    expect(init.mode).toBe("same-origin");
    expect(init.credentials).toBe("omit");
    expect(init.headers).toEqual({ "content-type": "application/json" });
    expect(JSON.parse(String(init.body))).toEqual({ code: "7H3K-9QWX", device_name: "Pixel 9" });
  });

  test("maps {error} bodies and 429 to messages", async () => {
    const limited = await pair(
      async () => Response.json({ error: "rate_limited" }, { status: 429 }),
      "c",
      "n",
    );
    expect(limited).toMatchObject({ ok: false, retryable: true });
    expect(limited.ok === false && limited.message).toContain("Too many");
    const invalid = await pair(
      async () => Response.json({ error: "invalid" }, { status: 400 }),
      "c",
      "n",
    );
    expect(invalid).toMatchObject({ ok: false, retryable: false });
    expect(invalid.ok === false && invalid.message).toContain("expired");
    const bare429 = await pair(async () => new Response("", { status: 429 }), "c", "n");
    expect(bare429).toMatchObject({ ok: false, retryable: true });
    const down = await pair(
      async () => {
        throw new Error("network");
      },
      "c",
      "n",
    );
    expect(down).toMatchObject({ ok: false, retryable: true });
    const odd = await pair(async () => Response.json({ token: "" }), "c", "n");
    expect(odd).toMatchObject({ ok: false, retryable: false });
  });

  test("a 429 carries Retry-After; a 415 is not a wrong code", async () => {
    const limited = await pair(
      async () =>
        Response.json({ error: "rate_limited" }, { status: 429, headers: { "retry-after": "30" } }),
      "c",
      "n",
    );
    expect(limited).toMatchObject({ ok: false, retryable: true, retryAfterMs: 30_000 });
    const media = await pair(async () => new Response("", { status: 415 }), "c", "n");
    expect(media.ok === false && media.message).toContain("content type");
    expect(media).toMatchObject({ retryAfterMs: null });
  });

  test("parseRetryAfter reads seconds and dates", () => {
    expect(parseRetryAfter("5", 0)).toBe(5_000);
    expect(parseRetryAfter(null, 0)).toBeNull();
    expect(parseRetryAfter("soon", 0)).toBeNull();
    const now = Date.parse("2026-10-13T10:00:00Z");
    expect(parseRetryAfter("Tue, 13 Oct 2026 10:00:10 GMT", now)).toBe(10_000);
    expect(parseRetryAfter("Tue, 13 Oct 2026 09:00:00 GMT", now)).toBe(0);
  });
});

test("wsUrl and deviceName", () => {
  expect(wsUrl({ protocol: "https:", host: "h:1" })).toBe("wss://h:1/api/v1/ws");
  expect(wsUrl({ protocol: "http:", host: "127.0.0.1:7733" })).toBe(
    "ws://127.0.0.1:7733/api/v1/ws",
  );
  expect(deviceName("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)")).toBe("iPhone");
  expect(deviceName("Mozilla/5.0 (Linux; Android 14; Pixel 9 Build/AD1A) Chrome")).toBe("Pixel 9");
  expect(deviceName("Mozilla/5.0 (Linux; Android 10; K) Chrome")).toBe("Android phone");
  expect(deviceName("curl")).toBe("Phone");
});
