import { useEffect, useState } from "react";
import { ActivityIndicator, Alert, ScrollView, Text, View } from "react-native";
import type { ChatSession } from "../../../../pkg/remote/web/src/chat3";
import type { AgentKind, Mode, ModelChoice } from "../../../../pkg/remote/web/src/protocol";
import type { SessionLike } from "../../../../pkg/remote/web/src/state3";
import { assertNativeAction } from "../native/passkeys";
import { Badge, Button, Group, Notice, Row, Segmented, Sheet } from "../ui/components";
import { Icon } from "../ui/icons";
import { useTheme } from "../ui/theme";

export interface SessionSheetProps {
  visible: boolean;
  onClose(): void;
  chat: ChatSession;
  session: SessionLike | null;
  relayUrl: string | null;
  // The composer's "Add agent" opens straight on the list of agent CLIs.
  startAt?: "main" | "kinds";
}

type Pane = { at: "main" } | { at: "models"; kind: string; adding: boolean } | { at: "kinds" };

const MODES: ReadonlyArray<{ value: Mode; label: string }> = [
  { value: "ask", label: "Ask" },
  { value: "plan", label: "Plan" },
  { value: "yolo", label: "Yolo" },
];

const MODE_HELP: Record<Mode, string> = {
  ask: "Agents ask before running commands or editing files.",
  plan: "Agents plan first and wait for your go-ahead.",
  yolo: "Agents act without asking. Turning it on needs your passkey.",
};

// The action digest is lowercase hex, as the computer's gate specifies.
async function yoloDigest(pj: string, sessionId: string): Promise<string> {
  const raw = await crypto.subtle.digest(
    "SHA-256",
    new TextEncoder().encode(`mode.set|yolo|${pj}|${sessionId}`),
  );
  return Array.from(new Uint8Array(raw), (b) => b.toString(16).padStart(2, "0")).join("");
}

export function SessionSheet({
  visible,
  onClose,
  chat,
  session,
  relayUrl,
  startAt = "main",
}: SessionSheetProps) {
  const { t, s } = useTheme();
  const [, tick] = useState(0);
  const [view, setView] = useState<Pane>({ at: "main" });
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [kinds, setKinds] = useState<AgentKind[] | null>(null);
  const [models, setModels] = useState<ModelChoice[] | null>(null);
  const online = session !== null;
  const allowYolo = session?.welcome.server.allow_yolo === true;
  useEffect(() => chat.subscribe(() => tick((n) => n + 1)), [chat]);
  useEffect(() => {
    if (!visible) {
      setView({ at: "main" });
      setError(null);
      return;
    }
    if (startAt !== "kinds") return;
    let active = true;
    setKinds(null);
    setView({ at: "kinds" });
    void chat
      .agentKinds()
      .then((found) => {
        if (active) setKinds(found);
      })
      .catch((failure: unknown) => {
        if (!active) return;
        setKinds([]);
        setError(failure instanceof Error ? failure.message : "Could not list agents.");
      });
    return () => {
      active = false;
    };
  }, [visible, startAt, chat]);

  async function run(work: () => Promise<void>): Promise<void> {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await work();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "The action failed.");
    } finally {
      setBusy(false);
    }
  }

  function pickMode(mode: Mode): void {
    if (mode === chat.mode) return;
    if (mode !== "yolo") {
      void run(() => chat.setMode(mode));
      return;
    }
    if (!allowYolo) {
      setError("Yolo isn't allowed for this phone. Grant it on your computer first.");
      return;
    }
    Alert.alert(
      "Turn on yolo?",
      "Agents will run commands and edit files in this conversation without asking.",
      [
        { text: "Cancel", style: "cancel" },
        {
          text: "Verify with passkey",
          style: "destructive",
          onPress: () =>
            void run(async () => {
              if (session === null || relayUrl === null) throw new Error("Reconnect first.");
              const digest = await yoloDigest(chat.pj, chat.sessionId);
              const assertion = await assertNativeAction(session, relayUrl, digest);
              await chat.setMode("yolo", assertion);
            }),
        },
      ],
    );
  }

  function openModels(kind: string, adding: boolean): void {
    setModels(null);
    setView({ at: "models", kind, adding });
    void chat
      .models(kind)
      .then(setModels)
      .catch((failure: unknown) => {
        setModels([]);
        setError(failure instanceof Error ? failure.message : "Could not load models.");
      });
  }

  function openKinds(): void {
    setKinds(null);
    setView({ at: "kinds" });
    void chat
      .agentKinds()
      .then(setKinds)
      .catch((failure: unknown) => {
        setKinds([]);
        setError(failure instanceof Error ? failure.message : "Could not list agents.");
      });
  }

  const title =
    view.at === "kinds"
      ? "Add a model"
      : view.at === "models"
        ? view.adding
          ? `Add ${view.kind}`
          : `${view.kind} model`
        : "Session";

  return (
    <Sheet visible={visible} onClose={onClose} title={title}>
      <ScrollView style={{ flexGrow: 0 }} contentContainerStyle={{ gap: 12, paddingBottom: 4 }}>
        {!online && <Notice>Reconnect to change this conversation.</Notice>}
        {error && <Notice error>{error}</Notice>}

        {view.at === "main" && (
          <>
            <Text style={s.label}>Permissions</Text>
            <Segmented
              label="Permissions"
              options={MODES}
              value={chat.mode}
              onChange={pickMode}
              disabled={!online || busy}
            />
            <Text style={[s.secondary, { marginHorizontal: 4 }]}>
              {MODE_HELP[chat.mode ?? "ask"]}
            </Text>
            <Text style={[s.label, { marginTop: 6 }]}>In this conversation</Text>
            <Group>
              {chat.roster.length === 0 && (
                <Row
                  title="No agents yet"
                  subtitle="Add a model, or send a message to start one."
                />
              )}
              {chat.roster.map((agent, i) => (
                <Row
                  key={agent.id}
                  divider={i > 0}
                  title={agent.name}
                  subtitle={[agent.model, agent.effort].filter(Boolean).join(" · ") || agent.kind}
                  onPress={online ? () => openModels(agent.kind, false) : undefined}
                  label={`${agent.name}, ${agent.model ?? agent.kind}. Change model`}
                  trailing={
                    agent.busy ? (
                      <Badge text="Working" color={t.info} />
                    ) : (
                      <Icon name="chevron-right" color={t.muted} size={15} weight={2.2} />
                    )
                  }
                />
              ))}
              <Row
                divider
                title="Add a model"
                onPress={online ? openKinds : undefined}
                leading={<Icon name="plus" color={t.primary} size={18} weight={2.2} />}
              />
            </Group>
          </>
        )}

        {view.at === "kinds" && (
          <>
            <Text style={[s.secondary, { marginHorizontal: 4 }]}>
              The new agent joins quietly and reads the conversation when someone first addresses
              it.
            </Text>
            <Group>
              {kinds === null ? (
                <View style={{ padding: 20 }}>
                  <ActivityIndicator color={t.primary} />
                </View>
              ) : kinds.length === 0 ? (
                <Row title="No agent CLIs found" subtitle="Install one on your computer first." />
              ) : (
                kinds.map((kind, i) => (
                  <Row
                    key={kind.kind}
                    divider={i > 0}
                    title={kind.kind}
                    subtitle={kind.model ?? "Default model"}
                    onPress={() => openModels(kind.kind, true)}
                    trailing={
                      kind.joined ? (
                        <Badge text="In chat" color={t.success} />
                      ) : (
                        <Icon name="chevron-right" color={t.muted} size={15} weight={2.2} />
                      )
                    }
                  />
                ))
              )}
            </Group>
            <Button title="Back" icon="back" size="sm" onPress={() => setView({ at: "main" })} />
          </>
        )}

        {view.at === "models" && (
          <>
            <Group>
              {models === null ? (
                <View style={{ padding: 20 }}>
                  <ActivityIndicator color={t.primary} />
                </View>
              ) : (
                <>
                  {view.adding && (
                    <Row
                      title="Default model"
                      subtitle="Whatever this computer is set to use"
                      onPress={() =>
                        void run(async () => {
                          await chat.addAgent(view.kind);
                          setView({ at: "main" });
                        })
                      }
                    />
                  )}
                  {models.map((choice, i) => (
                    <View key={choice.id}>
                      <Row
                        divider={i > 0 || view.adding}
                        title={choice.label}
                        subtitle={
                          choice.efforts?.length
                            ? `Effort: ${choice.efforts.join(", ")}`
                            : undefined
                        }
                        onPress={() =>
                          void run(async () => {
                            if (view.adding) await chat.addAgent(view.kind, choice.id);
                            else await chat.setModel(view.kind, choice.id);
                            setView({ at: "main" });
                          })
                        }
                        trailing={
                          choice.selected && !view.adding ? (
                            <Icon name="check" color={t.primary} size={18} weight={2.4} />
                          ) : undefined
                        }
                      />
                      {!view.adding && choice.selected && !!choice.efforts?.length && (
                        <View style={{ paddingHorizontal: 16, paddingBottom: 12 }}>
                          <Segmented
                            label="Effort"
                            options={choice.efforts.map((e) => ({ value: e, label: e }))}
                            value={chat.roster.find((a) => a.kind === view.kind)?.effort ?? null}
                            disabled={busy}
                            onChange={(effort) =>
                              void run(() => chat.setModel(view.kind, choice.id, effort))
                            }
                          />
                        </View>
                      )}
                    </View>
                  ))}
                </>
              )}
            </Group>
            <Button title="Back" icon="back" size="sm" onPress={() => setView({ at: "main" })} />
          </>
        )}
        {busy && <ActivityIndicator color={t.primary} />}
      </ScrollView>
    </Sheet>
  );
}
