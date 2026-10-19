import type { Controller } from "@web/controller";
import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { DeckBridge, SavedServer } from "../../shared/bridge";
import { connect } from "../connection";
import { ConnectScreen } from "./ConnectScreen";
import { Shell } from "./Shell";

interface Active {
  server: SavedServer;
  c: Controller;
}

const noSubscribe = () => () => {};

export function App({ bridge }: { bridge: DeckBridge }) {
  const [servers, setServers] = useState<SavedServer[] | null>(null);
  const [active, setActive] = useState<Active | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  const current = useRef<Active | null>(null);

  const refresh = useCallback(() => bridge.listServers().then(setServers), [bridge]);

  // One connection at a time. The old controller stops after the new one is
  // current, so its reset cannot be taken for the new one's.
  const swap = useCallback((next: Active | null) => {
    const prev = current.current;
    current.current = next;
    setActive(next);
    prev?.c.forget();
  }, []);

  const open = useCallback(
    (s: SavedServer) => {
      setMessage(null);
      swap({ server: s, c: connect(bridge, s.id) });
    },
    [bridge, swap],
  );

  const leave = useCallback(
    (why: string | null) => {
      swap(null);
      setMessage(why);
      void refresh();
    },
    [swap, refresh],
  );

  // The newest pairing opens on start.
  useEffect(() => {
    void bridge.listServers().then((list) => {
      setServers(list);
      const first = list[0];
      if (first !== undefined) open(first);
    });
  }, [bridge, open]);

  // The shared controller resets to "unpaired" when Deck refuses the token
  // (main has forgotten it too) and to "protocol" on a version mismatch.
  const auth = useSyncExternalStore(
    active === null ? noSubscribe : active.c.store.subscribe,
    () => (active === null ? null : active.c.state.auth),
  );
  useEffect(() => {
    if (active === null || active !== current.current) return;
    if (auth === "unpaired") leave(active.c.state.authMessage ?? "Deck ended this pairing. Pair again.");
    else if (auth === "protocol") leave("This Deck speaks another protocol version. Update Deck or this app.");
  }, [active, auth, leave]);

  return (
    <div className="app" data-app="">
      {active !== null ? (
        <Shell key={active.server.id} c={active.c} server={active.server} onDisconnect={() => leave(null)} />
      ) : servers === null ? (
        <div className="loading">Loading…</div>
      ) : (
        <ConnectScreen
          bridge={bridge}
          servers={servers}
          message={message}
          onPaired={(s) => {
            void refresh();
            open(s);
          }}
          onOpen={open}
          onForget={(s) => void bridge.forgetServer(s.id).then(refresh)}
        />
      )}
    </div>
  );
}
