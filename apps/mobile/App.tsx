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
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  ActivityIndicator,
  AppState,
  ScrollView,
  StatusBar,
  Text,
  TextInput,
  View,
} from "react-native";
import { SafeAreaProvider, SafeAreaView } from "react-native-safe-area-context";
import { ChatSession } from "../../pkg/remote/web/src/chat3";
import type { LoadedSnapshot } from "../../pkg/remote/web/src/offline/snapshot";
import {
  type ApprovalV3,
  decodeApprovalV3,
  decodeReceipt,
  type Receipt,
} from "../../pkg/remote/web/src/protocol3";
import type { SessionLike } from "../../pkg/remote/web/src/state3";
import { NativeClient } from "./src/core/client";
import { assertNativeAction } from "./src/native/passkeys";
import { AppearanceScreen } from "./src/screens/AppearanceScreen";
import { ApprovalPanel } from "./src/screens/ApprovalPanel";
import { ChatScreen } from "./src/screens/ChatScreen";
import { ComputersScreen } from "./src/screens/ComputersScreen";
import { InboxScreen } from "./src/screens/InboxScreen";
import { OfflineApprovals } from "./src/screens/OfflineApprovals";
import { PairScreen } from "./src/screens/PairScreen";
import { type QueueRow, QueueScreen } from "./src/screens/QueueScreen";
import { SessionControls } from "./src/screens/SessionControls";
import { kindTones } from "./src/ui/agents";
import { Button, Notice, ScreenHeader } from "./src/ui/components";
import { ThemeProvider, useTheme } from "./src/ui/ThemeContext";

type Routes = {
  Computers: undefined;
  Appearance: undefined;
  Pair: { initialUrl?: string } | undefined;
  Inbox: { cid: string };
  Chat: { cid: string; pj: string; sessionId: string };
  Queue: { cid: string };
};
const Stack = createNativeStackNavigator<Routes>();
const navigation = createNavigationContainerRef<Routes>();
const ClientContext = createContext<NativeClient | null>(null);

function useClient(): NativeClient {
  const client = useContext(ClientContext);
  const [, update] = useState(0);
  useEffect(() => client?.subscribe(() => update((n) => n + 1)), [client]);
  if (client === null) throw new Error("Deck is still starting.");
  return client;
}
const message = (error: unknown): string =>
  error instanceof Error ? error.message : "The action could not be completed.";

function ComputersRoute({ navigation: nav }: NativeStackScreenProps<Routes, "Computers">) {
  const { styles } = useTheme();
  const client = useClient();
  const [error, setError] = useState<string | null>(null);
  return (
    <View style={styles.screen}>
      {(error || client.lastError) && <Notice error>{error || client.lastError}</Notice>}
      <ComputersScreen
        computers={client.views()}
        onSelect={(cid) => nav.navigate("Inbox", { cid })}
        onPair={() => nav.navigate("Pair")}
        onAppearance={() => nav.navigate("Appearance")}
        onRetry={(cid) => {
          void client.connect(cid).catch((err: unknown) => setError(message(err)));
        }}
      />
    </View>
  );
}

function AppearanceRoute({ navigation: nav }: NativeStackScreenProps<Routes, "Appearance">) {
  return <AppearanceScreen onBack={() => nav.goBack()} />;
}

function PairRoute({ navigation: nav, route }: NativeStackScreenProps<Routes, "Pair">) {
  const client = useClient();
  return (
    <PairScreen
      initialUrl={route.params?.initialUrl}
      onBack={() => nav.goBack()}
      onPair={async (url, name) => {
        const record = await client.pair(url, name);
        nav.replace("Inbox", { cid: record.cid });
      }}
    />
  );
}

function InboxRoute({ navigation: nav, route }: NativeStackScreenProps<Routes, "Inbox">) {
  const { styles } = useTheme();
  const client = useClient();
  const computer = client.get(route.params.cid);
  const [snapshotEntry, setSnapshot] = useState<{
    cid: string;
    value: LoadedSnapshot | null;
  } | null>(null);
  const snapshot = snapshotEntry?.cid === route.params.cid ? snapshotEntry.value : null;
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    let active = true;
    if (computer?.status === "offline" || computer?.status === "connecting") {
      void client
        .loadSnapshot(route.params.cid)
        .then((value) => {
          if (active) setSnapshot({ cid: route.params.cid, value });
        })
        .catch((err: unknown) => {
          if (active) setError(message(err));
        });
    }
    return () => {
      active = false;
    };
  }, [client, route.params.cid, computer?.status]);
  if (!computer) return <Unavailable onBack={() => nav.goBack()} />;
  const saved =
    snapshot !== null && (computer.status === "offline" || computer.status === "connecting");
  const view = saved
    ? {
        ...computer,
        inbox: snapshot.snapshot.inbox,
        lastSeen: snapshot.computer.lastSeen ?? computer.lastSeen,
      }
    : computer;
  return (
    <View style={styles.screen}>
      {error && <Notice error>{error}</Notice>}
      {saved && (
        <Notice>
          Saved activity from {new Date(snapshot.snapshot.at).toLocaleString()}. Reconnect for
          current status.
        </Notice>
      )}
      <InboxScreen
        computer={view}
        onBack={() => nav.goBack()}
        onOpenQueue={() => nav.navigate("Queue", route.params)}
        onSelect={(pj, sessionId) => {
          if (computer.status === "online")
            void client.ack(route.params.cid, pj, sessionId).catch(() => undefined);
          nav.navigate("Chat", { ...route.params, pj, sessionId });
        }}
        onNewSession={(pj) => {
          const session = client.session(route.params.cid);
          if (!session) {
            setError("Reconnect before starting a new conversation.");
            return;
          }
          void session
            .call<{ id: string }>("sessions.create", {}, pj)
            .then((created) => nav.navigate("Chat", { ...route.params, pj, sessionId: created.id }))
            .catch((err: unknown) => setError(message(err)));
        }}
      >
        {saved && (
          <OfflineApprovals client={client} cid={route.params.cid} snapshot={snapshot.snapshot} />
        )}
      </InboxScreen>
    </View>
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
    setApprovals([]);
    // Events and completed answers explicitly trigger a fresh server read.
    void revision;
    void session
      .call<unknown>("approvals.list", { session_id: sessionId }, pj)
      .then((value) => {
        if (!Array.isArray(value)) throw new Error("Could not read the pending approvals.");
        const parsed = value.map(decodeApprovalV3);
        if (parsed.some((approval) => approval === null || approval.session_id !== sessionId))
          throw new Error("Unreadable approval. Reconnect before answering.");
        if (active) {
          setApprovals(parsed.filter((approval): approval is ApprovalV3 => approval !== null));
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
  return (
    <View style={{ gap: 12 }}>
      {error && <Notice error>{error}</Notice>}
      {record && (
        <SessionControls
          session={session}
          relayUrl={record.relayUrl}
          pj={pj}
          sessionId={sessionId}
        />
      )}
      {approvals.map((approval, index) => (
        <ApprovalPanel
          key={approval.id}
          approval={approval}
          index={index}
          count={approvals.length}
          online
          authenticate={
            record ? () => assertNativeAction(session, record.relayUrl, approval.digest) : undefined
          }
          onAnswer={async (decision, reason, assertion) => {
            await session.call(
              "approvals.answer",
              {
                session_id: sessionId,
                id: approval.id,
                decision,
                ...(reason ? { reason } : {}),
                ...(assertion ? { assertion } : {}),
              },
              pj,
            );
            setRevision((n) => n + 1);
          }}
        />
      ))}
    </View>
  );
}

function ChatRoute({ navigation: nav, route }: NativeStackScreenProps<Routes, "Chat">) {
  const { styles, tokens } = useTheme();
  const client = useClient();
  const { cid, pj, sessionId } = route.params;
  const computer = client.get(cid);
  const session = client.session(cid);
  const tones = useMemo(() => kindTones(session?.welcome.server.theme), [session]);
  const [binding, setChat] = useState<{
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
  useEffect(() => {
    let active = true;
    let current: ChatSession | undefined;
    void retry;
    setError(null);
    if (session !== null) {
      void ChatSession.open(session, pj, sessionId)
        .then((opened) => {
          current = opened;
          if (active) setChat({ cid, chat: opened, session });
          else opened.detach();
        })
        .catch((err: unknown) => {
          if (active) setError(message(err));
        });
    }
    return () => {
      active = false;
      current?.detach();
    };
  }, [session, pj, sessionId, retry, cid]);
  if (!computer) return <Unavailable onBack={() => nav.goBack()} />;
  const queue = async (text: string, hours = 8): Promise<void> => {
    await client.queue(cid, {
      kind: "chat.send",
      pj,
      sessionId,
      payload: { text },
      expiresInMs: hours * 3_600_000,
    });
  };
  if (chat !== null)
    return (
      <View style={styles.screen}>
        {error && <Notice error>{error}</Notice>}
        <ChatScreen
          key={`${cid}:${pj}:${sessionId}`}
          chat={chat}
          tones={tones}
          online={computer.status === "online" && session !== null && binding?.session === session}
          onBack={() => nav.goBack()}
          onOpenQueue={() => nav.navigate("Queue", { cid })}
          onQueuePrompt={queue}
          onOpenSession={(id) => nav.push("Chat", { cid, pj, sessionId: id })}
        >
          {session !== null && (
            <LiveApprovals
              key={`${cid}:${pj}:${sessionId}`}
              session={session}
              cid={cid}
              pj={pj}
              sessionId={sessionId}
            />
          )}
        </ChatScreen>
      </View>
    );
  return (
    <ScrollView
      style={styles.screen}
      contentContainerStyle={styles.content}
      keyboardShouldPersistTaps="handled"
    >
      <ScreenHeader title="Conversation" onBack={() => nav.goBack()} />
      {error && <Notice error>{error}</Notice>}
      {computer.status === "online" ? (
        <>
          <ActivityIndicator color={tokens.accent} />
          <Button title="Retry loading" onPress={() => setRetry((n) => n + 1)} />
        </>
      ) : (
        <>
          <Notice>
            The computer is offline. Its transcript is not cached on this phone. You can queue a
            prompt for when it returns.
          </Notice>
          <OfflineComposer onQueue={queue} />
        </>
      )}
      <Button title="Queue and receipts" onPress={() => nav.navigate("Queue", { cid })} />
    </ScrollView>
  );
}

function OfflineComposer({ onQueue }: { onQueue(text: string, hours: number): Promise<void> }) {
  const { styles, tokens } = useTheme();
  const [text, setText] = useState("");
  const [hours, setHours] = useState(8);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  return (
    <View style={{ gap: 12 }}>
      <TextInput
        style={styles.input}
        accessibilityLabel="Prompt to queue"
        multiline
        value={text}
        onChangeText={setText}
        placeholder="Prompt for when the computer returns"
        placeholderTextColor={tokens.muted}
        editable={!busy}
        maxLength={60_000}
      />
      <Text style={styles.secondary}>Expires after</Text>
      <View style={{ flexDirection: "row", gap: 8 }}>
        {[1, 8, 24].map((value) => (
          <Button
            key={value}
            title={`${value}h`}
            tone={hours === value ? "primary" : "default"}
            onPress={() => setHours(value)}
            disabled={busy}
          />
        ))}
      </View>
      <Button
        title="Queue prompt"
        busy={busy}
        disabled={!text.trim()}
        onPress={() => {
          setBusy(true);
          setNotice(null);
          void onQueue(text.trim(), hours)
            .then(() => {
              setText("");
              setNotice("Queued. Check receipts after the computer reconnects.");
            })
            .catch((err: unknown) => setNotice(message(err)))
            .finally(() => setBusy(false));
        }}
      />
      {notice && <Notice>{notice}</Notice>}
    </View>
  );
}

function QueueRoute({ navigation: nav, route }: NativeStackScreenProps<Routes, "Queue">) {
  const { styles } = useTheme();
  const client = useClient();
  const { cid } = route.params;
  const session = client.session(cid);
  const [rows, setRows] = useState<QueueRow[]>([]);
  const [receipts, setReceipts] = useState<Receipt[]>([]);
  const [error, setError] = useState<string | null>(null);
  const refreshGeneration = useRef(0);
  const refresh = useCallback(async (): Promise<void> => {
    const generation = ++refreshGeneration.current;
    const [outbox, history] = await Promise.all([client.outbox(cid), client.history(cid)]);
    let latest: Receipt[] = [];
    if (session !== null) {
      const raw = await session.call<unknown>("queue.status");
      if (Array.isArray(raw))
        latest = raw.map(decodeReceipt).filter((r): r is Receipt => r !== null);
    } else latest = (await client.loadSnapshot(cid))?.snapshot.receipts ?? [];
    if (generation !== refreshGeneration.current) return;
    setReceipts(latest);
    setRows([
      ...outbox.map(
        (entry): QueueRow => ({
          qid: entry.qid,
          kind: entry.kind ?? "Queued action",
          state: "outbox",
          createdAt: entry.createdAt,
          expiresAt: entry.expires_at,
        }),
      ),
      ...history
        .filter((entry) => !latest.some((r) => r.qid === entry.qid))
        .map(
          (entry): QueueRow => ({
            qid: entry.qid,
            kind: entry.kind,
            state: "sent",
            createdAt: entry.created_at,
            expiresAt: entry.expires_at,
          }),
        ),
    ]);
  }, [client, cid, session]);
  useEffect(() => {
    let active = true;
    let timer: ReturnType<typeof setTimeout> | undefined;
    void refresh().catch((err: unknown) => {
      if (active) setError(message(err));
    });
    const update = (): void => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        if (active)
          void refresh().catch((err: unknown) => {
            if (active) setError(message(err));
          });
      }, 150);
    };
    const unsubscribeClient = client.subscribe(update);
    const unsubscribeEvents = session?.onEvent((event) => {
      if (active && event.name === "receipts.changed") update();
    });
    return () => {
      active = false;
      refreshGeneration.current++;
      clearTimeout(timer);
      unsubscribeClient();
      if (typeof unsubscribeEvents === "function") unsubscribeEvents();
    };
  }, [client, session, refresh]);
  return (
    <View style={styles.screen}>
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
  const { styles } = useTheme();
  return (
    <View style={[styles.screen, styles.content]}>
      <Notice>This computer is no longer paired on this phone.</Notice>
      <Button title="Back" onPress={onBack} />
    </View>
  );
}

export default function App() {
  return (
    <ThemeProvider>
      <AppShell />
    </ThemeProvider>
  );
}

function AppShell() {
  const { styles, tokens } = useTheme();
  const base = tokens.mode === "dark" ? DarkTheme : DefaultTheme;
  const [client, setClient] = useState<NativeClient | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const [pendingLink, setPendingLink] = useState<string | null>(null);
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
  return (
    <SafeAreaProvider>
      <StatusBar
        barStyle={tokens.mode === "dark" ? "light-content" : "dark-content"}
        backgroundColor={tokens.background}
      />
      <SafeAreaView style={styles.screen}>
        {client === null ? (
          <View style={[styles.content, { flex: 1, justifyContent: "center" }]}>
            <Text style={styles.title}>Deck</Text>
            {error ? (
              <>
                <Notice error>{error}</Notice>
                <Button title="Retry startup" onPress={() => setAttempt((n) => n + 1)} />
              </>
            ) : (
              <>
                <ActivityIndicator color={tokens.accent} />
                <Text style={styles.secondary}>Opening your secure workspace…</Text>
              </>
            )}
          </View>
        ) : (
          <ClientContext.Provider value={client}>
            <NavigationContainer
              ref={navigation}
              theme={{
                ...base,
                colors: {
                  ...base.colors,
                  background: tokens.background,
                  card: tokens.surface,
                  text: tokens.text,
                  primary: tokens.accent,
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
                screenOptions={{
                  headerShown: false,
                  contentStyle: { backgroundColor: tokens.background },
                }}
              >
                <Stack.Screen name="Computers" component={ComputersRoute} />
                <Stack.Screen name="Appearance" component={AppearanceRoute} />
                <Stack.Screen name="Pair" component={PairRoute} />
                <Stack.Screen name="Inbox" component={InboxRoute} />
                <Stack.Screen name="Chat" component={ChatRoute} />
                <Stack.Screen name="Queue" component={QueueRoute} />
              </Stack.Navigator>
            </NavigationContainer>
          </ClientContext.Provider>
        )}
      </SafeAreaView>
    </SafeAreaProvider>
  );
}
