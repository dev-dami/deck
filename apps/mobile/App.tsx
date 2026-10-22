import {
  createNavigationContainerRef,
  DarkTheme,
  DefaultTheme,
  NavigationContainer,
} from "@react-navigation/native";
import {
  createNativeStackNavigator,
  type NativeStackScreenProps,
} from "@react-navigation/native-stack";
import * as Linking from "expo-linking";
import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { ActivityIndicator, AppState, StatusBar, Text, View } from "react-native";
import { SafeAreaProvider, SafeAreaView } from "react-native-safe-area-context";
import { ChatSession } from "../../pkg/remote/web/src/chat3";
import type { LoadedSnapshot } from "../../pkg/remote/web/src/offline/snapshot";
import { type ChangedFile, decodeChangedFile } from "../../pkg/remote/web/src/protocol";
import {
  type ApprovalV3,
  decodeApprovalV3,
  decodeReceipt,
  type Receipt,
} from "../../pkg/remote/web/src/protocol3";
import type { SessionLike } from "../../pkg/remote/web/src/state3";
import { NativeClient } from "./src/core/client";
import { assertNativeAction } from "./src/native/passkeys";
import { ApprovalPanel } from "./src/screens/ApprovalPanel";
import { ChangesScreen } from "./src/screens/ChangesScreen";
import { ChatScreen } from "./src/screens/ChatScreen";
import { HomeScreen } from "./src/screens/HomeScreen";
import { OfflineApprovals } from "./src/screens/OfflineApprovals";
import { PairScreen } from "./src/screens/PairScreen";
import { type QueueRow, QueueScreen } from "./src/screens/QueueScreen";
import { SessionSheet } from "./src/screens/SessionSheet";
import { ComputersSheet, SettingsSheet } from "./src/screens/SettingsSheets";
import { Button, IconButton, Notice, TopBar } from "./src/ui/components";
import { ThemeProvider, useTheme } from "./src/ui/theme";

type Routes = {
  Home: undefined;
  Pair: { initialUrl?: string } | undefined;
  Chat: { cid: string; pj: string; sessionId: string };
  Changes: { cid: string; pj: string; sessionId: string };
  Queue: { cid: string };
};
const Stack = createNativeStackNavigator<Routes>();
const navigation = createNavigationContainerRef<Routes>();
const ClientContext = createContext<NativeClient | null>(null);
const CurrentContext = createContext<{ cid: string | null; select(cid: string): void }>({
  cid: null,
  select: () => undefined,
});

function useClient(): NativeClient {
  const client = useContext(ClientContext);
  const [, update] = useState(0);
  useEffect(() => client?.subscribe(() => update((n) => n + 1)), [client]);
  if (client === null) throw new Error("Deck is still starting.");
  return client;
}

const message = (error: unknown): string =>
  error instanceof Error ? error.message : "The action could not be completed.";

function HomeRoute({ navigation: nav }: NativeStackScreenProps<Routes, "Home">) {
  const client = useClient();
  const current = useContext(CurrentContext);
  const views = client.views();
  const computer = views.find((v) => v.record.cid === current.cid) ?? views[0] ?? null;
  const cid = computer?.record.cid ?? null;
  const [sheet, setSheet] = useState<"computers" | "settings" | null>(null);
  const [snapshot, setSnapshot] = useState<{ cid: string; value: LoadedSnapshot | null } | null>(
    null,
  );
  const [waiting, setWaiting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const status = computer?.status;

  useEffect(() => {
    let active = true;
    if (cid !== null && (status === "offline" || status === "connecting"))
      void client
        .loadSnapshot(cid)
        .then((value) => {
          if (active) setSnapshot({ cid, value });
        })
        .catch((err: unknown) => {
          if (active) setError(message(err));
        });
    return () => {
      active = false;
    };
  }, [client, cid, status]);

  useEffect(() => {
    let active = true;
    if (cid !== null)
      void client
        .outbox(cid)
        .then((rows) => {
          if (active) setWaiting(rows.length > 0);
        })
        .catch(() => {
          if (active) setWaiting(false);
        });
    return () => {
      active = false;
    };
  });

  const saved =
    computer !== null && snapshot?.cid === cid && snapshot.value !== null && status !== "online"
      ? snapshot.value
      : null;
  const view =
    computer && saved
      ? {
          ...computer,
          inbox: saved.snapshot.inbox,
          lastSeen: saved.computer.lastSeen ?? computer.lastSeen,
        }
      : computer;
  const session = cid === null ? null : client.session(cid);

  return (
    <>
      <HomeScreen
        computer={view}
        waiting={waiting}
        onOpenComputers={() => setSheet("computers")}
        onOpenSettings={() => setSheet("settings")}
        onOpenActivity={() => cid && nav.navigate("Queue", { cid })}
        onPair={() => nav.navigate("Pair")}
        onRetry={() => {
          if (cid) void client.connect(cid).catch((err: unknown) => setError(message(err)));
        }}
        onOpenTask={(pj, sessionId) => {
          if (cid === null) return;
          if (status === "online") void client.ack(cid, pj, sessionId).catch(() => undefined);
          nav.navigate("Chat", { cid, pj, sessionId });
        }}
        onStartTask={async (pj, text) => {
          if (cid === null || session === null)
            throw new Error("Reconnect before starting a task.");
          const created = await session.call<{ id: string }>("sessions.create", {}, pj);
          const chat = await client.chat(cid, pj, created.id);
          try {
            await chat.send(text);
          } finally {
            chat.detach();
          }
          nav.navigate("Chat", { cid, pj, sessionId: created.id });
        }}
      >
        {error && <Notice error>{error}</Notice>}
        {client.lastError && <Notice error>{client.lastError}</Notice>}
        {saved && cid && <OfflineApprovals client={client} cid={cid} snapshot={saved.snapshot} />}
      </HomeScreen>
      <ComputersSheet
        visible={sheet === "computers"}
        onClose={() => setSheet(null)}
        computers={views}
        current={cid}
        onSelect={(next) => {
          current.select(next);
          setSheet(null);
          const picked = client.get(next);
          if (picked && picked.status !== "online" && picked.status !== "connecting")
            void client.connect(next).catch((err: unknown) => setError(message(err)));
        }}
        onPair={() => {
          setSheet(null);
          nav.navigate("Pair");
        }}
      />
      <SettingsSheet
        visible={sheet === "settings"}
        onClose={() => setSheet(null)}
        session={session}
        relayUrl={computer?.record.relayUrl ?? null}
        computers={views.length}
        onOpenComputers={() => setSheet("computers")}
      />
    </>
  );
}

function PairRoute({ navigation: nav, route }: NativeStackScreenProps<Routes, "Pair">) {
  const client = useClient();
  const current = useContext(CurrentContext);
  return (
    <PairScreen
      initialUrl={route.params?.initialUrl}
      onBack={() => nav.goBack()}
      onPair={async (url, name) => {
        const record = await client.pair(url, name);
        current.select(record.cid);
        nav.popToTop();
      }}
    />
  );
}

function LiveApprovals({
  session,
  cid,
  pj,
  sessionId,
}: {
  session: SessionLike;
  cid: string;
  pj: string;
  sessionId: string;
}) {
  const client = useClient();
  const [approvals, setApprovals] = useState<ApprovalV3[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [revision, setRevision] = useState(0);
  const record = client.get(cid)?.record;
  useEffect(() => {
    let active = true;
    const unsubscribe = session.onEvent((event) => {
      if (
        active &&
        (event.name === "resync" ||
          (event.pj === pj &&
            (event.name === "approval.requested" || event.name === "approval.resolved")))
      )
        setRevision((n) => n + 1);
    });
    return () => {
      active = false;
      if (typeof unsubscribe === "function") unsubscribe();
    };
  }, [session, pj]);
  useEffect(() => {
    let active = true;
    // Events and completed answers explicitly trigger a fresh server read.
    void revision;
    void session
      .call<unknown>("approvals.list", { session_id: sessionId }, pj)
      .then((value) => {
        if (!Array.isArray(value)) throw new Error("Could not read the pending approvals.");
        const parsed = value.map(decodeApprovalV3);
        if (parsed.some((a) => a === null || a.session_id !== sessionId))
          throw new Error("Unreadable approval. Reconnect before answering.");
        if (active) {
          setApprovals(parsed.filter((a): a is ApprovalV3 => a !== null));
          setError(null);
        }
      })
      .catch((err: unknown) => {
        if (active) setError(message(err));
      });
    return () => {
      active = false;
    };
  }, [session, pj, sessionId, revision]);
  const first = approvals[0];
  if (error) return <Notice error>{error}</Notice>;
  if (!first) return null;
  return (
    <ApprovalPanel
      key={first.id}
      approval={first}
      more={approvals.length - 1}
      online
      authenticate={
        record ? () => assertNativeAction(session, record.relayUrl, first.digest) : undefined
      }
      onAnswer={async (decision, reason, assertion) => {
        await session.call(
          "approvals.answer",
          {
            session_id: sessionId,
            id: first.id,
            decision,
            ...(reason ? { reason } : {}),
            ...(assertion ? { assertion } : {}),
          },
          pj,
        );
        setRevision((n) => n + 1);
      }}
    />
  );
}

function ChatRoute({ navigation: nav, route }: NativeStackScreenProps<Routes, "Chat">) {
  const client = useClient();
  const { s, t } = useTheme();
  const { cid, pj, sessionId } = route.params;
  const computer = client.get(cid);
  const session = client.session(cid);
  const [binding, setBinding] = useState<{
    cid: string;
    chat: ChatSession;
    session: SessionLike;
  } | null>(null);
  const chat =
    binding?.cid === cid && binding.chat.pj === pj && binding.chat.sessionId === sessionId
      ? binding.chat
      : null;
  const [error, setError] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);
  const [sheet, setSheet] = useState(false);
  useEffect(() => {
    let active = true;
    let opened: ChatSession | undefined;
    void retry;
    setError(null);
    if (session !== null)
      void ChatSession.open(session, pj, sessionId)
        .then((value) => {
          opened = value;
          if (active) setBinding({ cid, chat: value, session });
          else value.detach();
        })
        .catch((err: unknown) => {
          if (active) setError(message(err));
        });
    return () => {
      active = false;
      opened?.detach();
    };
  }, [session, pj, sessionId, retry, cid]);
  if (!computer) return <Unavailable onBack={() => nav.goBack()} />;
  const title =
    computer.inbox.find((i) => i.session_id === sessionId)?.title.trim() || "Conversation";
  const queue = async (text: string): Promise<void> => {
    await client.queue(cid, {
      kind: "chat.send",
      pj,
      sessionId,
      payload: { text },
      expiresInMs: 8 * 3_600_000,
    });
  };
  if (chat === null)
    return (
      <View style={s.screen}>
        <TopBar
          title={title}
          left={<IconButton icon="back" label="Back to tasks" onPress={() => nav.goBack()} />}
        />
        <View style={{ padding: 16, gap: 12 }}>
          {error && <Notice error>{error}</Notice>}
          {computer.status === "online" ? (
            <>
              <ActivityIndicator color={t.primary} />
              {error && (
                <Button title="Retry" icon="refresh" onPress={() => setRetry((n) => n + 1)} />
              )}
            </>
          ) : (
            <Notice>
              This computer is offline and the conversation isn't saved on this phone. Reconnect
              from the task list.
            </Notice>
          )}
        </View>
      </View>
    );
  const online = computer.status === "online" && session !== null && binding?.session === session;
  return (
    <>
      <ChatScreen
        key={`${cid}:${pj}:${sessionId}`}
        chat={chat}
        title={title}
        online={online}
        onBack={() => nav.goBack()}
        onOpenChanges={() => nav.navigate("Changes", { cid, pj, sessionId })}
        onOpenSession={() => setSheet(true)}
        onQueuePrompt={queue}
        docked={
          session !== null ? (
            <LiveApprovals
              key={`${cid}:${pj}:${sessionId}`}
              session={session}
              cid={cid}
              pj={pj}
              sessionId={sessionId}
            />
          ) : undefined
        }
      />
      <SessionSheet
        visible={sheet}
        onClose={() => setSheet(false)}
        chat={chat}
        session={online ? session : null}
        relayUrl={computer.record.relayUrl}
      />
    </>
  );
}

function ChangesRoute({ navigation: nav, route }: NativeStackScreenProps<Routes, "Changes">) {
  const client = useClient();
  const { cid, pj, sessionId } = route.params;
  const session = client.session(cid);
  const [files, setFiles] = useState<ChangedFile[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let active = true;
    if (session === null) {
      setLoading(false);
      setError("Reconnect to see the changes.");
      return;
    }
    // The full diff, not the summary the conversation shows.
    void session
      .call<unknown>("changes.get", { session_id: sessionId }, pj)
      .then((value) => {
        if (!active) return;
        const list = Array.isArray(value) ? value.map(decodeChangedFile) : [];
        setFiles(list.filter((f): f is ChangedFile => f !== null));
      })
      .catch((err: unknown) => {
        if (active) setError(message(err));
      })
      .finally(() => {
        if (active) setLoading(false);
      });
    return () => {
      active = false;
    };
  }, [session, pj, sessionId]);
  return (
    <ChangesScreen files={files} loading={loading} error={error} onBack={() => nav.goBack()} />
  );
}

function QueueRoute({ navigation: nav, route }: NativeStackScreenProps<Routes, "Queue">) {
  const client = useClient();
  const { cid } = route.params;
  const session = client.session(cid);
  const [rows, setRows] = useState<QueueRow[]>([]);
  const [receipts, setReceipts] = useState<Receipt[]>([]);
  const [error, setError] = useState<string | null>(null);
  const generation = useRef(0);
  const refresh = useCallback(async (): Promise<void> => {
    const mine = ++generation.current;
    const [outbox, history] = await Promise.all([client.outbox(cid), client.history(cid)]);
    let latest: Receipt[] = [];
    if (session !== null) {
      const raw = await session.call<unknown>("queue.status");
      if (Array.isArray(raw))
        latest = raw.map(decodeReceipt).filter((r): r is Receipt => r !== null);
    } else latest = (await client.loadSnapshot(cid))?.snapshot.receipts ?? [];
    if (mine !== generation.current) return;
    setReceipts(latest);
    setRows([
      ...outbox.map(
        (e): QueueRow => ({
          qid: e.qid,
          kind: e.kind ?? "Queued action",
          state: "outbox",
          createdAt: e.createdAt,
          expiresAt: e.expires_at,
        }),
      ),
      ...history
        .filter((e) => !latest.some((r) => r.qid === e.qid))
        .map(
          (e): QueueRow => ({
            qid: e.qid,
            kind: e.kind,
            state: "sent",
            createdAt: e.created_at,
            expiresAt: e.expires_at,
          }),
        ),
    ]);
  }, [client, cid, session]);
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const run = (): void => {
      void refresh().catch((err: unknown) => {
        if (active) setError(message(err));
      });
    };
    run();
    const update = (): void => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (active) run();
      }, 150);
    };
    const offClient = client.subscribe(update);
    const offEvents = session?.onEvent((event) => {
      if (active && event.name === "receipts.changed") update();
    });
    return () => {
      active = false;
      generation.current++;
      clearTimeout(timer);
      offClient();
      if (typeof offEvents === "function") offEvents();
    };
  }, [client, session, refresh]);
  return (
    <View style={{ flex: 1 }}>
      {error && <Notice error>{error}</Notice>}
      <QueueScreen
        rows={rows}
        receipts={receipts}
        onBack={() => nav.goBack()}
        onRefresh={async () => {
          await client.flush(cid);
          await refresh();
        }}
        onCancel={async (qid) => {
          await client.cancel(cid, qid);
          await refresh();
        }}
      />
    </View>
  );
}

function Unavailable({ onBack }: { onBack(): void }) {
  const { s } = useTheme();
  return (
    <View style={[s.screen, s.content]}>
      <Notice>This computer is no longer paired with this phone.</Notice>
      <Button title="Back" onPress={onBack} />
    </View>
  );
}

function Shell() {
  const { t, s } = useTheme();
  const [client, setClient] = useState<NativeClient | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [pendingLink, setPendingLink] = useState<string | null>(null);
  const [cid, setCid] = useState<string | null>(null);
  useEffect(() => {
    let active = true;
    let running: NativeClient | null = null;
    void attempt;
    setError(null);
    void NativeClient.bootstrap()
      .then((created) => {
        running = created;
        if (!active) {
          created.stop();
          return;
        }
        setClient(created);
        if (AppState.currentState === "active" || AppState.currentState === null) created.start();
      })
      .catch((err: unknown) => {
        if (active) setError(message(err));
      });
    const listener = AppState.addEventListener("change", (state) => {
      if (state === "active") running?.start();
      // An OS credential sheet can temporarily make the app inactive or
      // backgrounded. Pause retries, but let the OS manage the existing pipe
      // so a valid passkey response can still finish its authenticated RPC.
      else if (state === "background") running?.fleet.stopReconnect();
    });
    return () => {
      active = false;
      running?.stop();
      listener.remove();
    };
  }, [attempt]);
  useEffect(() => {
    const accept = (url: string | null): void => {
      if (url && (url.includes("#p=") || url.startsWith("deck-mobile://pair"))) setPendingLink(url);
    };
    void Linking.getInitialURL()
      .then(accept)
      .catch(() => undefined);
    const listener = Linking.addEventListener("url", ({ url }) => accept(url));
    return () => listener.remove();
  }, []);
  useEffect(() => {
    if (client && pendingLink && navigation.isReady()) {
      navigation.navigate("Pair", { initialUrl: pendingLink });
      setPendingLink(null);
    }
  }, [client, pendingLink]);
  const base = t.dark ? DarkTheme : DefaultTheme;
  return (
    <>
      <StatusBar barStyle={t.dark ? "light-content" : "dark-content"} backgroundColor={t.bg} />
      <SafeAreaView style={s.screen}>
        {client === null ? (
          <View style={[s.content, { flex: 1, justifyContent: "center" }]}>
            <Text style={s.title}>Deck</Text>
            {error ? (
              <>
                <Notice error>{error}</Notice>
                <Button title="Retry startup" onPress={() => setAttempt((n) => n + 1)} />
              </>
            ) : (
              <>
                <ActivityIndicator color={t.primary} />
                <Text style={s.secondary}>Opening your secure workspace…</Text>
              </>
            )}
          </View>
        ) : (
          <ClientContext.Provider value={client}>
            <CurrentContext.Provider value={{ cid, select: setCid }}>
              <NavigationContainer
                ref={navigation}
                theme={{
                  ...base,
                  colors: {
                    ...base.colors,
                    background: t.bg,
                    card: t.card,
                    text: t.fg,
                    primary: t.primary,
                    border: t.line,
                  },
                }}
                onReady={() => {
                  if (pendingLink) {
                    navigation.navigate("Pair", { initialUrl: pendingLink });
                    setPendingLink(null);
                  }
                }}
              >
                <Stack.Navigator
                  screenOptions={{ headerShown: false, contentStyle: { backgroundColor: t.bg } }}
                >
                  <Stack.Screen name="Home" component={HomeRoute} />
                  <Stack.Screen name="Pair" component={PairRoute} />
                  <Stack.Screen name="Chat" component={ChatRoute} />
                  <Stack.Screen name="Changes" component={ChangesRoute} />
                  <Stack.Screen name="Queue" component={QueueRoute} />
                </Stack.Navigator>
              </NavigationContainer>
            </CurrentContext.Provider>
          </ClientContext.Provider>
        )}
      </SafeAreaView>
    </>
  );
}

export default function App() {
  return (
    <SafeAreaProvider>
      <ThemeProvider>
        <Shell />
      </ThemeProvider>
    </SafeAreaProvider>
  );
}
