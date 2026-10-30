import { describe, expect, test, vi } from "vitest";
import type { BuildResult } from "./artifactBuild";
import { artifactHost } from "./artifactHost";

const pane = () => ({ show: vi.fn((_entry: unknown, _request: string) => true), bounds: vi.fn(), hide: vi.fn(), close: vi.fn() });
const ok: BuildResult = { ok: true, js: "J", css: "C" };
const one = { request: "r1", kind: "react" as const, entrypoint: "A.tsx", files: [{ path: "A.tsx", content: "" }] };

describe("artifactHost", () => {
  test("shows HTML without building", async () => {
    const p = pane();
    const build = vi.fn();
    const h = artifactHost(p, build);
    await expect(h.show({ request: "r1", kind: "html", entrypoint: "i.html", files: [{ path: "i.html", content: "x" }] })).resolves.toEqual({ ok: true });
    expect(build).not.toHaveBeenCalled();
    expect(p.show).toHaveBeenCalledOnce();
    expect(p.show.mock.calls[0]?.[1]).toBe("r1");
  });

  test("a missing entry point is a diagnostic, not a crash", async () => {
    const h = artifactHost(pane(), vi.fn());
    const r = await h.show({ request: "r1", kind: "svg", entrypoint: "a.svg", files: [] });
    expect(r.ok).toBe(false);
  });

  test("a failed build shows nothing and returns its diagnostics", async () => {
    const p = pane();
    const diagnostics = [{ file: "A.tsx", line: 1, column: 2, message: "bad" }];
    const h = artifactHost(p, async () => ({ ok: false, diagnostics }));
    await expect(h.show(one)).resolves.toEqual({ ok: false, diagnostics });
    expect(p.show).not.toHaveBeenCalled();
  });

  test("a build that a later show overtook is dropped", async () => {
    const p = pane();
    let release: (r: BuildResult) => void = () => {};
    const slow = new Promise<BuildResult>((done) => {
      release = done;
    });
    const h = artifactHost(p, vi.fn().mockReturnValueOnce(slow).mockResolvedValueOnce(ok));
    const first = h.show(one);
    await expect(h.show(one)).resolves.toEqual({ ok: true });
    release(ok);
    await expect(first).resolves.toEqual({ ok: false, diagnostics: [] });
    expect(p.show).toHaveBeenCalledOnce();
  });

  test("closing drops a build in flight", async () => {
    const p = pane();
    const h = artifactHost(p, async () => ok);
    const pending = h.show(one);
    h.close();
    await expect(pending).resolves.toEqual({ ok: false, diagnostics: [] });
    expect(p.close).toHaveBeenCalled();
  });

  test("replacement and close abort compiler work, including replacement by HTML", async () => {
    const signals: AbortSignal[] = [];
    const h = artifactHost(pane(), async (_entry, _files, signal) => {
      signals.push(signal!);
      return new Promise<BuildResult>((resolve) => signal!.addEventListener("abort", () => resolve(ok), { once: true }));
    });
    const first = h.show(one);
    await h.show({ request: "html", kind: "html", entrypoint: "i.html", files: [{ path: "i.html", content: "" }] });
    expect(signals[0]?.aborted).toBe(true);
    await expect(first).resolves.toEqual({ ok: false, diagnostics: [] });
    const second = h.show(one);
    h.close();
    expect(signals[1]?.aborted).toBe(true);
    await expect(second).resolves.toEqual({ ok: false, diagnostics: [] });
  });
});
