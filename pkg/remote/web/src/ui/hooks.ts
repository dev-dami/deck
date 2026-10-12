import { createContext } from "preact";
import { useContext, useEffect, useState } from "preact/hooks";
import type { Controller } from "../controller";
import { parseRoute, type Route } from "../route";
import type { AppState } from "../state";

export const ControllerContext = createContext<Controller | null>(null);

export function useController(): Controller {
  const c = useContext(ControllerContext);
  if (c === null) throw new Error("ControllerContext is missing");
  return c;
}

export function useAppState(): AppState {
  const c = useController();
  const [state, setState] = useState(c.state);
  useEffect(() => {
    // Catch a change that landed between render and subscribe.
    setState(c.state);
    return c.store.subscribe(() => setState(c.state));
  }, [c]);
  return state;
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(location.hash));
  useEffect(() => {
    const on = () => setRoute(parseRoute(location.hash));
    window.addEventListener("hashchange", on);
    return () => window.removeEventListener("hashchange", on);
  }, []);
  return route;
}

export function useNow(intervalMs = 30_000): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), intervalMs);
    return () => clearInterval(id);
  }, [intervalMs]);
  return now;
}

// Runs an async loader whenever `key` changes; exposes loading/error/data.
export interface Loaded<T> {
  data: T | null;
  loading: boolean;
  reload: () => void;
}

export function useLoad<T>(load: () => Promise<T | null>, key: string): Loaded<T> {
  const [data, setData] = useState<T | null>(null);
  const [loading, setLoading] = useState(true);
  const [tick, setTick] = useState(0);
  // `load` is intentionally keyed by `key`/`tick`: callers pass fresh closures.
  useEffect(() => {
    let live = true;
    setLoading(true);
    load().then((d) => {
      if (!live) return;
      setData(d);
      setLoading(false);
    });
    return () => {
      live = false;
    };
  }, [key, tick]);
  return { data, loading, reload: () => setTick((t) => t + 1) };
}

export function navigate(hash: string): void {
  location.hash = hash;
}
