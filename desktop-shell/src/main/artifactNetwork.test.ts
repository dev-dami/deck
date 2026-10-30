import { connect } from "node:net";
import type { Session } from "electron";
import { expect, test, vi } from "vitest";
import { lockArtifactSession } from "./artifact";

test("artifact proxy owns its port and refuses bytes instead of relying on a free port", async () => {
  const setProxy = vi.fn(async (_config: { proxyRules: string; proxyBypassRules: string }) => {});
  const ses = {
    setPermissionRequestHandler: vi.fn(),
    setPermissionCheckHandler: vi.fn(),
    setDevicePermissionHandler: vi.fn(),
    on: vi.fn(),
    webRequest: { onBeforeRequest: vi.fn() },
    setProxy,
  } as unknown as Session;
  await lockArtifactSession(ses, "deck-artifact");
  const config = setProxy.mock.calls[0]?.[0] as { proxyRules: string; proxyBypassRules: string };
  const url = new URL(config.proxyRules);
  expect(url.hostname).toBe("127.0.0.1");
  expect(Number(url.port)).toBeGreaterThan(1);
  expect(config.proxyBypassRules).toBe("<-loopback>");
  const received = await new Promise<number>((resolve, reject) => {
    let bytes = 0;
    const socket = connect({ host: url.hostname, port: Number(url.port) });
    socket.setTimeout(2000, () => { socket.destroy(); reject(new Error("refusal endpoint did not close")); });
    socket.on("data", (chunk) => { bytes += chunk.length; });
    socket.on("error", (error: NodeJS.ErrnoException) => { if (error.code !== "ECONNRESET") reject(error); });
    socket.on("close", () => resolve(bytes));
  });
  expect(received).toBe(0);
});
