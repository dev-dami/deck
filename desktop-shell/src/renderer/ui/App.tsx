import { useCallback, useEffect, useRef, useState, useSyncExternalStore } from "react";
import type { DeckBridge, LocalDeck, SavedServer } from "../../shared/bridge";
import { useAppearance } from "../appearance";
import type { DeckClient } from "../client";
import { connect, openLocal } from "../connection";
import { usePrefs } from "../prefs";
import { ConnectScreen } from "./ConnectScreen";
import { Shell } from "./Shell";
import type { ProjectEntry } from "./Sidebar";

interface Active {
  key: string;
  // Where the Deck is, for the sidebar.
  where: string;
  // The sidebar's project entry it came from: "folder:<dir>" or "paired:<id>".
  source: string;
  transport: string;
  c: DeckClient;
}

// The last part of a folder path.
function base(dir: string): string {
  return dir.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || dir;
}

const noSubscribe = () => () => {};

function hostPort(s: SavedServer): string {
  return `${s.host.includes(":") ? `[${s.host}]` : s.host}:${s.port}`;
}

export function App({ bridge }: { bridge: DeckBridge }) {
  const [servers, setServers] = useState<SavedServer[] | null>(null);
  const [recent, setRecent] = useState<string[]>([]);
  const [deck, setDeck] = useState<string | null>(null);
  const [active, setActive] = useState<Active | null>(null);
  const [message, setMessage] = useState<string | null>(null);
  // The folder whose Deck is starting.
  const [opening, setOpening] = useState<string | null>(null);
  const current = useRef<Active | null>(null);

  const refresh = useCallback(async () => {
    const [s, r] = await Promise.all([bridge.listServers(), bridge.recentFolders()]);
    setServers(s);
    setRecent(r);
  }, [bridge]);

  // One Deck at a time. The old client stops after the new one is current,
  // so its reset cannot be taken for the new one's.
  const swap = useCallback((next: Active | null) => {
    const prev = current.current;
    current.current = next;
    setActive(next);
    prev?.c.forget();
  }, []);

  const pairedOpen = useCallback(
    (s: SavedServer) => {
      setMessage(null);
      swap({
        key: `v2:${s.id}`,
        where: hostPort(s),
        source: `paired:${s.id}`,
        transport: "Paired over WebSocket (deck remote, protocol v2)",
        c: connect(bridge, s.id),
      });
    },
    [bridge, swap],
  );

  const localOpen = useCallback(
    (d: LocalDeck) => {
      setMessage(null);
      swap({
        key: `rpc:${d.id}`,
        where: d.dir,
        source: `folder:${d.dir}`,
        transport: "Local folder over gRPC (deck serve --rpc)",
        c: openLocal(bridge, d),
      });
    },
    [bridge, swap],
  );

  // Starts (or finds) the Deck of a folder; the error text, or null.
  const openFolder = useCallback(
    async (dir: string): Promise<string | null> => {
      setOpening(dir);
      const out = await bridge.openFolder(dir);
      setOpening(null);
      void refresh();
      if (!out.ok) return out.message;
      localOpen(out.deck);
      return null;
    },
    [bridge, localOpen, refresh],
  );

  const leave = useCallback(
    (why: string | null) => {
      swap(null);
      setMessage(why);
      void refresh();
    },
    [swap, refresh],
  );

  // On start: the last folder when deck is on PATH, else the newest pairing.
  useEffect(() => {
    void (async () => {
      const [s, r, d] = await Promise.all([bridge.listServers(), bridge.recentFolders(), bridge.findDeck()]);
      setServers(s);
      setRecent(r);
      setDeck(d);
      const folder = r[0];
      const paired = s[0];
      if (d !== null && folder !== undefined) {
        const err = await openFolder(folder);
        if (err !== null) setMessage(err);
      } else if (paired !== undefined) pairedOpen(paired);
    })();
  }, [bridge, openFolder, pairedOpen]);

  // A client resets to "unpaired" when its Deck ends the connection for good
  // (a refused token over v2, a stopped process over rpc) and to "protocol"
  // on a version mismatch.
  const auth = useSyncExternalStore(
    active === null ? noSubscribe : active.c.store.subscribe,
    () => (active === null ? null : active.c.state.auth),
  );
  useEffect(() => {
    if (active === null || active !== current.current) return;
    if (auth === "unpaired") leave(active.c.state.authMessage ?? "Deck ended this connection.");
    else if (auth === "protocol") leave("This Deck speaks another protocol version. Update Deck or this app.");
  }, [active, auth, leave]);

  const prefs = usePrefs();
  useAppearance(prefs.theme);

  const projects: ProjectEntry[] = [
    ...recent.map((dir) => ({
      key: `folder:${dir}`,
      name: base(dir),
      detail: dir,
      kind: "folder" as const,
      active: active?.source === `folder:${dir}`,
    })),
    ...(servers ?? []).map((s) => ({
      key: `paired:${s.id}`,
      name: hostPort(s),
      detail: s.tls ? "https, pinned" : "this computer",
      kind: "paired" as const,
      active: active?.source === `paired:${s.id}`,
    })),
  ];

  const toProject = async (key: string): Promise<string | null> => {
    if (key.startsWith("folder:")) return openFolder(key.slice("folder:".length));
    const s = servers?.find((x) => `paired:${x.id}` === key);
    if (s !== undefined) pairedOpen(s);
    return null;
  };

  const addProject = async (): Promise<string | null> => {
    if (deck === null) return "Put the deck command on your PATH to open a folder.";
    const dir = await bridge.pickFolder();
    return dir === null ? null : openFolder(dir);
  };

  return (
    <div className="app h-full" data-app="">
      {active !== null ? (
        <Shell
          key={active.key}
          c={active.c}
          where={active.where}
          transport={active.transport}
          projects={projects}
          onProject={toProject}
          onAddProject={addProject}
          onDisconnect={() => leave(null)}
        />
      ) : servers === null ? (
        <div className="loading grid h-full place-items-center text-muted-foreground text-sm">Loading…</div>
      ) : (
        <ConnectScreen
          bridge={bridge}
          deck={deck}
          servers={servers}
          recent={recent}
          opening={opening}
          message={message}
          onFolder={openFolder}
          onPaired={(s) => {
            void refresh();
            pairedOpen(s);
          }}
          onOpen={pairedOpen}
          onForget={(s) => void bridge.forgetServer(s.id).then(refresh)}
        />
      )}
    </div>
  );
}
