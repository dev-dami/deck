import { describe, expect, test } from "vitest";
import { failureMessage, needsRelayDir, parsePhone, PhoneLink, type Run, type RunResult } from "./phone";

const status = {
  name: "pc",
  relay_url: "http://192.168.1.5:8787",
  online: true,
  devices: 1,
  device_list: [{ id: "d1", name: "Pixel" }],
  projects: 2,
};

describe("parsePhone", () => {
  test("reads the engine's JSON line", () => {
    expect(parsePhone(`${JSON.stringify(status)}\n`)).toEqual({
      name: "pc",
      relayUrl: "http://192.168.1.5:8787",
      online: true,
      projects: 2,
      devices: [{ id: "d1", name: "Pixel" }],
      pairUrl: null,
      qr: null,
      expiresAt: null,
      message: null,
    });
  });

  test("carries the pairing link, QR and expiry", () => {
    const p = parsePhone(
      JSON.stringify({ ...status, pair_url: "http://x/#p=1", qr: ["010", "101"], expires_at: "2026-10-22T12:00:00Z" }),
    );
    expect(p?.pairUrl).toBe("http://x/#p=1");
    expect(p?.qr).toEqual(["010", "101"]);
    expect(p?.expiresAt).toBe("2026-10-22T12:00:00Z");
  });

  test("ignores noise around the JSON and rejects what is not a status", () => {
    expect(parsePhone(`warming up\n${JSON.stringify(status)}\n`)?.name).toBe("pc");
    expect(parsePhone("not json")).toBeNull();
    expect(parsePhone('{"name":"pc"}')).toBeNull();
  });

  test("drops a QR that is not rows of modules", () => {
    expect(parsePhone(JSON.stringify({ ...status, qr: ["01x"] }))?.qr).toBeNull();
    expect(parsePhone(JSON.stringify({ ...status, qr: [] }))?.qr).toBeNull();
  });

  test("skips devices without an id", () => {
    const p = parsePhone(JSON.stringify({ ...status, device_list: [{ name: "x" }, { id: "d2", name: "Moto" }] }));
    expect(p?.devices).toEqual([{ id: "d2", name: "Moto" }]);
  });
});

describe("failures", () => {
  test("takes cobra's Error line", () => {
    expect(failureMessage("Error: port 8787 is busy\nUsage:\n  deck", 1)).toBe("port 8787 is busy");
  });

  test("falls back to the first stderr line, then to the exit code", () => {
    expect(failureMessage("boom\nmore", 1)).toBe("boom");
    expect(failureMessage("", 3)).toBe("Deck exited with code 3.");
    expect(failureMessage("", null)).toBe("Deck could not run the phone link.");
  });

  test("recognises a missing relay runtime", () => {
    expect(needsRelayDir("local relay runtime not found; use --relay-dir pointing to pkg/remote/relay")).toBe(true);
    expect(needsRelayDir("port 8787 is busy")).toBe(false);
  });
});

describe("PhoneLink", () => {
  const ok = (body: object): RunResult => ({ stdout: `${JSON.stringify(body)}\n`, stderr: "", code: 0 });

  function recording(result: RunResult): { run: Run; calls: string[][] } {
    const calls: string[][] = [];
    return {
      calls,
      run: async (args) => {
        calls.push(args);
        return result;
      },
    };
  }

  test("link shares the folder and uses the recorded relay folder", async () => {
    const { run, calls } = recording(ok(status));
    const out = await new PhoneLink(run, "/repo/pkg/remote/relay").link("/work/app", null);
    expect(out.ok).toBe(true);
    expect(calls[0]).toEqual([
      "mobile", "connect", "--json", "--action", "link", "--project", "/work/app", "--relay-dir", "/repo/pkg/remote/relay",
    ]);
  });

  test("a chosen relay folder beats the recorded one, and no folder shares nothing", async () => {
    const { run, calls } = recording(ok(status));
    await new PhoneLink(run, "/recorded").link(null, "/chosen");
    expect(calls[0]).toEqual(["mobile", "connect", "--json", "--action", "link", "--relay-dir", "/chosen"]);
  });

  test("status, new code, revoke and stop map to the engine's actions", async () => {
    const { run, calls } = recording(ok(status));
    const phone = new PhoneLink(run);
    await phone.status();
    await phone.newCode();
    await phone.revoke("d1");
    await phone.stop();
    expect(calls.map((c) => c.slice(3))).toEqual([
      ["--action", "status"],
      ["--action", "pair"],
      ["--action", "revoke", "--device", "d1"],
      ["--action", "stop"],
    ]);
  });

  test("a failed run reports the engine's message and whether the relay folder is needed", async () => {
    const { run } = recording({
      stdout: "",
      stderr: "Error: local relay runtime not found; use --relay-dir pointing to pkg/remote/relay\n",
      code: 1,
    });
    expect(await new PhoneLink(run).link(null, null)).toEqual({
      ok: false,
      message: "local relay runtime not found; use --relay-dir pointing to pkg/remote/relay",
      needsRelayDir: true,
    });
  });

  test("an answer that is not a status is an error, not a crash", async () => {
    const { run } = recording({ stdout: "hello\n", stderr: "", code: 0 });
    const out = await new PhoneLink(run).status();
    expect(out.ok).toBe(false);
  });

  test("without an engine nothing runs", async () => {
    const out = await new PhoneLink(null).status();
    expect(out).toMatchObject({ ok: false, needsRelayDir: false });
  });
});
