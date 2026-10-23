import type { JSX } from "preact";
import { render } from "preact";
import { useEffect, useState } from "preact/hooks";
import { trackKeyboard } from "../keyboard";
import { flushOutbox, queueAction } from "../offline/queue";
import type { LoadedSnapshot } from "../offline/snapshot";
import type { QRBlob } from "../protocol3";
import { registerHostedWorker, subscribePush } from "../push/subscribe";
import { FleetState } from "../state3";
import { ApprovalPanel } from "./ApprovalPanel";
import { ChatScreen } from "./ChatScreen";
import { ComputersScreen } from "./ComputersScreen";
import { InboxScreen } from "./InboxScreen";
import { OfflineApprovals } from "./OfflineApprovals";
import { OfflinePanel } from "./OfflinePanel";
import { PairingScreen } from "./PairingScreen";
import { stripPairFragment } from "./pairing";
import { chatHash, readPairOffer, routeOf } from "./startup";

function App(props: {
  fleet: FleetState;
  offer: { blob: QRBlob | null; error: string | null };
}): JSX.Element {
  const { fleet } = props;
  const [, setTick] = useState(0);
  const [offer, setOffer] = useState(props.offer);
  const [route, setRoute] = useState(() => routeOf(location.hash));
  const [error, setError] = useState<string | null>(null);
  const [snapshot, setSnapshot] = useState<LoadedSnapshot | null>(null);
  const [worker, setWorker] = useState<ServiceWorkerRegistration | null>(null);
  const report = (err: unknown): void =>
    setError(err instanceof Error ? err.message : "The action failed.");
  useEffect(() => fleet.subscribe(() => setTick((n) => n + 1)), [fleet]);
  useEffect(() => {
    const onHash = (): void => {
      if (location.hash.startsWith("#p="))
        setOffer(readPairOffer(location.hash, () => stripPairFragment()));
      else {
        setRoute(routeOf(location.hash));
        setSnapshot(null);
        setError(null);
      }
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);
  useEffect(() => {
    let active = true;
    const restore = async (): Promise<void> => {
      await fleet.reload();
      if (!active) return;
      fleet.startReconnect();
      for (const view of fleet.views()) {
        if (view.record.relayToken !== null) void flushOutbox(view.record).catch(report);
      }
    };
    void restore().catch(report);
    const online = (): void => {
      void restore().catch(report);
    };
    window.addEventListener("online", online);
    const visible = (): void => {
      if (document.visibilityState === "visible") online();
    };
    document.addEventListener("visibilitychange", visible);
    void registerHostedWorker()
      .then(async (registration) => {
        if (registration !== null) {
          const ready = await navigator.serviceWorker.ready;
          if (active) setWorker(ready);
        }
      })
      .catch(report);
    return () => {
      active = false;
      fleet.stopReconnect();
      window.removeEventListener("online", online);
      document.removeEventListener("visibilitychange", visible);
    };
  }, [fleet]);
  if (offer.blob !== null)
    return (
      <PairingScreen
        blob={offer.blob}
        onCancel={() => {
          setOffer({ blob: null, error: null });
          location.hash = "#/";
        }}
        onPaired={(record) => {
          setOffer({ blob: null, error: null });
          void fleet
            .reload()
            .then(() => fleet.connect(record.cid))
            .then(() => {
              location.hash = `#/c/${record.cid}`;
            })
            .catch(report);
        }}
      />
    );
  const computer = route.name !== "computers" ? fleet.get(route.cid) : null;
  const session = computer !== null ? fleet.session(computer.record.cid) : null;
  const back = (): void => {
    location.hash = "#/";
  };
  const alerts = (
    <>
      {offer.error && <p role="alert">{offer.error}</p>}
      {error && <p role="alert">{error}</p>}
    </>
  );
  if (route.name !== "computers" && computer === null)
    return (
      <main>
        {alerts}
        <p>Computer not found.</p>
        <button type="button" onClick={back}>
          Computers
        </button>
      </main>
    );
  if (route.name === "chat" && computer !== null)
    return (
      <>
        {alerts}
        <ChatScreen
          key={`${route.cid}:${route.pj}:${route.sessionId}`}
          session={session}
          pj={route.pj}
          sessionId={route.sessionId}
          online={computer.status === "online"}
          onBack={() => {
            location.hash = `#/c/${route.cid}`;
          }}
          onOpenSession={(id) => {
            location.hash = chatHash(route.cid, route.pj, id);
          }}
          onQueuePrompt={async (text) => {
            await queueAction(computer.record, {
              kind: "chat.send",
              pj: route.pj,
              sessionId: route.sessionId,
              payload: { text },
            });
          }}
        >
          {session !== null && (
            <ApprovalPanel session={session} pj={route.pj} sessionId={route.sessionId} />
          )}
        </ChatScreen>
      </>
    );
  if (route.name === "inbox" && computer !== null) {
    const showSnapshot =
      (computer.status === "offline" || computer.status === "connecting") && snapshot !== null;
    const view =
      showSnapshot && snapshot !== null
        ? {
            ...computer,
            inbox: snapshot.snapshot.inbox,
            lastSeen: snapshot.computer.lastSeen ?? computer.lastSeen,
          }
        : computer;
    return (
      <>
        {alerts}
        <InboxScreen
          computer={view}
          nowMs={Date.now()}
          onBack={back}
          onAck={(pj, id) => {
            void fleet.ack(route.cid, pj, id).catch(report);
          }}
          onSelect={(pj, id) => {
            location.hash = chatHash(route.cid, pj, id);
          }}
          onRetry={() => {
            void fleet.connect(route.cid).catch(report);
          }}
        />
        {snapshot !== null && showSnapshot && (
          <p class="snapshot-label">
            Saved snapshot from {new Date(snapshot.snapshot.at).toLocaleString()}
            {snapshot.stale ? " · cached on this phone" : ""}
          </p>
        )}
        {snapshot !== null && showSnapshot && (
          <OfflineApprovals computer={computer.record} snapshot={snapshot.snapshot} />
        )}
        {session !== null && (
          <section aria-label="Computer actions">
            <button
              type="button"
              disabled={worker === null}
              onClick={() => {
                if (worker === null) return;
                void subscribePush(computer.record, session, worker)
                  .then(() => setError("Notifications enabled."))
                  .catch(report);
              }}
            >
              Enable notifications
            </button>
            <form
              onSubmit={(event) => {
                event.preventDefault();
                const data = new FormData(event.currentTarget);
                const pj = String(data.get("project") ?? "");
                void session
                  .call<{ id: string }>("sessions.create", {}, pj)
                  .then((result) => {
                    location.hash = chatHash(route.cid, pj, result.id);
                  })
                  .catch(report);
              }}
            >
              <label>
                Project
                <select name="project" required>
                  {computer.projects
                    .filter((p) => p.online)
                    .map((p) => (
                      <option key={p.id} value={p.id}>
                        {p.name}
                      </option>
                    ))}
                </select>
              </label>
              <button type="submit" disabled={!computer.projects.some((p) => p.online)}>
                New session
              </button>
            </form>
          </section>
        )}
        <OfflinePanel
          key={route.cid}
          computer={computer}
          session={session}
          onSnapshot={setSnapshot}
        />
      </>
    );
  }
  return (
    <>
      {alerts}
      <ComputersScreen
        computers={fleet.views()}
        onSelect={(cid) => {
          location.hash = `#/c/${cid}`;
        }}
        onRetry={(cid) => {
          void fleet.connect(cid).catch(report);
        }}
      />
    </>
  );
}
function boot(): void {
  const offer = readPairOffer(location.hash, () => stripPairFragment());
  const root = document.getElementById("app");
  if (root === null) throw new Error("#app is missing");
  trackKeyboard();
  render(
    <div class="hosted-app">
      <App fleet={new FleetState()} offer={offer} />
    </div>,
    root,
  );
}
boot();
