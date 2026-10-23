import { expect, test } from "bun:test";
import { trackKeyboard } from "./keyboard";

type Listener = () => void;

function fakeEnv(height: number) {
  const listeners = new Map<string, Listener>();
  const vv = {
    height,
    addEventListener: (type: string, fn: Listener) => listeners.set(type, fn),
    removeEventListener: (type: string) => listeners.delete(type),
  };
  const win = {
    visualViewport: vv,
    scrollY: 0,
    scrolledTo: null as number | null,
    scrollTo(_x: number, y: number) {
      this.scrolledTo = y;
      this.scrollY = y;
    },
  };
  const props = new Map<string, string>();
  const root = {
    style: {
      setProperty: (k: string, v: string) => props.set(k, v),
      removeProperty: (k: string) => props.delete(k),
    },
  };
  return { listeners, vv, win, root, props };
}

test("app height follows the visual viewport and scroll is reset", () => {
  const env = fakeEnv(800);
  const stop = trackKeyboard(env.win as unknown as Window, env.root as unknown as HTMLElement);
  expect(env.props.get("--app-height")).toBe("800px");

  env.vv.height = 450;
  env.win.scrollY = 120;
  env.listeners.get("resize")?.();
  expect(env.props.get("--app-height")).toBe("450px");
  expect(env.win.scrolledTo).toBe(0);

  stop();
  expect(env.props.has("--app-height")).toBe(false);
  expect(env.listeners.size).toBe(0);
});

test("no visualViewport is a no-op", () => {
  const win = { visualViewport: null } as unknown as Window;
  expect(() => trackKeyboard(win, {} as HTMLElement)()).not.toThrow();
});
