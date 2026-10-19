import { FlashList } from "@shopify/flash-list";
import { memo, type ReactNode, useEffect, useRef, useState } from "react";
import {
  KeyboardAvoidingView,
  Modal,
  Platform,
  ScrollView,
  Text,
  TextInput,
  View,
} from "react-native";
import { type ChatSession, newIdempotencyKey } from "../../../../pkg/remote/web/src/chat3";
import type { Message, ModelChoice } from "../../../../pkg/remote/web/src/protocol";
import { Button, EmptyState, Notice, ScreenHeader } from "../ui/components";
import { styles, tokens } from "../ui/theme";

export interface ChatScreenProps {
  chat: ChatSession;
  online: boolean;
  onBack(): void;
  onOpenQueue(): void;
  onQueuePrompt?(text: string): Promise<void>;
  onOpenSession?(sessionId: string): void;
  children?: ReactNode;
}

const MessageRow = memo(function MessageRow({ message }: { message: Message }) {
  const own = message.role === "user";
  const occurrences = new Map<string, number>();
  return (
    <View style={{ paddingHorizontal: 20, paddingVertical: 10 }}>
      <View
        style={{
          padding: 16,
          borderRadius: 16,
          gap: 10,
          backgroundColor: own ? tokens.elevated : tokens.surface,
          borderWidth: 1,
          borderColor: tokens.border,
        }}
      >
        <View style={styles.between}>
          <Text style={[styles.eyebrow, { color: own ? tokens.accent : tokens.secondary }]}>
            {own ? "You" : message.agent || message.role}
          </Text>
          <Text style={[styles.secondary, { fontSize: 11 }]}>
            {new Date(message.created_at).toLocaleTimeString([], {
              hour: "2-digit",
              minute: "2-digit",
            })}
          </Text>
        </View>
        {message.parts.map((part) => {
          const identity = `${part.type}:${part.id ?? part.tool_call_id ?? part.name ?? part.image?.name ?? "body"}`;
          const occurrence = occurrences.get(identity) ?? 0;
          occurrences.set(identity, occurrence + 1);
          const key = `${identity}:${occurrence}`;
          const text = part.text ?? part.output ?? part.diff ?? part.summary;
          if (text !== undefined)
            return (
              <View key={key} style={{ gap: 4 }}>
                {part.name && <Text style={styles.eyebrow}>{part.name}</Text>}
                <Text
                  selectable
                  style={
                    part.type === "text"
                      ? styles.body
                      : [styles.code, part.is_error ? { color: tokens.danger } : {}]
                  }
                >
                  {text.slice(0, 24_000)}
                </Text>
                {(part.text_truncated ||
                  part.output_truncated ||
                  part.diff_truncated ||
                  text.length > 24_000) && (
                  <Text style={styles.secondary}>
                    Preview truncated. Review the complete output on your computer.
                  </Text>
                )}
              </View>
            );
          if (part.image)
            return (
              <Text key={key} style={styles.secondary}>
                Attachment · {part.image.name}
              </Text>
            );
          return (
            <Text key={key} style={styles.secondary}>
              {part.name || part.type}
            </Text>
          );
        })}
        {!message.finished && (
          <Text accessibilityLiveRegion="polite" style={{ color: tokens.accent, fontSize: 12 }}>
            Writing…
          </Text>
        )}
      </View>
    </View>
  );
});

export function ChatScreen({
  chat,
  online,
  onBack,
  onOpenQueue,
  onQueuePrompt,
  onOpenSession,
  children,
}: ChatScreenProps) {
  const [, tick] = useState(0);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [panel, setPanel] = useState<"models" | "changes" | null>(null);
  const [choices, setChoices] = useState<ModelChoice[]>([]);
  const [kind, setKind] = useState("");
  const retrySend = useRef<{ text: string; key: string } | null>(null);
  const acting = useRef(false);
  const identity = `${chat.pj}\0${chat.sessionId}`;
  const draftIdentity = useRef(identity);
  const modelBinding = useRef(chat);
  const modelRequest = useRef(0);
  const live = online && !chat.resyncing;
  useEffect(() => {
    const unsubscribe = chat.subscribe(() => tick((value) => value + 1));
    return () => {
      modelRequest.current++;
      unsubscribe();
    };
  }, [chat]);
  useEffect(() => {
    if (draftIdentity.current === identity) return;
    draftIdentity.current = identity;
    setText("");
    setError(null);
    retrySend.current = null;
  }, [identity]);
  useEffect(() => {
    if (modelBinding.current === chat) return;
    modelBinding.current = chat;
    modelRequest.current++;
    setPanel(null);
    setChoices([]);
  }, [chat]);
  async function act(action: () => Promise<unknown>): Promise<void> {
    if (acting.current) return;
    acting.current = true;
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "The action failed.");
    } finally {
      acting.current = false;
      setBusy(false);
    }
  }
  async function send(): Promise<void> {
    if (!text.trim()) return;
    const value = text;
    await act(async () => {
      if (online) {
        if (!live) throw new Error("Wait for the conversation to finish syncing.");
        if (retrySend.current?.text !== value.trim())
          retrySend.current = { text: value.trim(), key: newIdempotencyKey() };
        await chat.send(value, retrySend.current.key);
        retrySend.current = null;
      } else if (onQueuePrompt) await onQueuePrompt(value);
      else throw new Error("Reconnect to send this message.");
      setText((current) => (current === value ? "" : current));
    });
  }
  async function loadModels(selected: string): Promise<void> {
    const request = ++modelRequest.current;
    const binding = chat;
    setKind(selected);
    setChoices([]);
    await act(async () => {
      try {
        const models = await binding.models(selected);
        if (modelRequest.current === request && modelBinding.current === binding)
          setChoices(models);
      } catch (failure) {
        if (modelRequest.current === request && modelBinding.current === binding) throw failure;
      }
    });
  }
  return (
    <KeyboardAvoidingView
      style={styles.screen}
      behavior={Platform.OS === "ios" ? "padding" : undefined}
    >
      <ScreenHeader
        title="Conversation"
        subtitle={
          online
            ? chat.busy
              ? "Agent is working"
              : "Ready when you are"
            : "Offline · prompts can wait for your computer"
        }
        onBack={onBack}
      />
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        style={{ flexGrow: 0 }}
        contentContainerStyle={{ paddingHorizontal: 20, gap: 8, paddingBottom: 12 }}
      >
        <Button
          title={chat.busy ? "Interrupt" : "Continue"}
          tone={chat.busy ? "danger" : "default"}
          disabled={!live || busy}
          onPress={() => void act(() => (chat.busy ? chat.interrupt() : chat.cont()))}
        />
        <Button
          title="New session"
          disabled={!live || busy || !onOpenSession}
          onPress={() =>
            void act(async () => {
              const id = await chat.createSession();
              onOpenSession?.(id);
            })
          }
        />
        <Button
          title="Models"
          disabled={!live || busy}
          onPress={() => {
            setPanel("models");
            const first = chat.roster[0]?.kind;
            if (first) void loadModels(first);
          }}
        />
        <Button
          title={`Changes${chat.changes.length ? ` · ${chat.changes.length}` : ""}`}
          onPress={() => {
            setPanel("changes");
            if (live) void act(() => chat.refreshChanges());
          }}
        />
        <Button title="Queue" onPress={onOpenQueue} />
      </ScrollView>
      {(error || chat.error) && (
        <View style={{ paddingHorizontal: 20, paddingBottom: 10 }}>
          <Notice error>{error || chat.error}</Notice>
          {chat.error && (
            <Button
              title="Retry conversation sync"
              busy={busy}
              disabled={!online || chat.resyncing}
              onPress={() => void act(() => chat.refresh())}
            />
          )}
        </View>
      )}
      {children && (
        <ScrollView
          nestedScrollEnabled
          style={{ flexGrow: 0, maxHeight: "35%" }}
          contentContainerStyle={{ paddingHorizontal: 20, paddingBottom: 12, gap: 12 }}
        >
          {children}
        </ScrollView>
      )}
      <FlashList
        data={chat.messages}
        keyExtractor={(message) => message.id}
        renderItem={({ item }) => <MessageRow message={item} />}
        maintainVisibleContentPosition={{
          startRenderingFromBottom: true,
          autoscrollToBottomThreshold: 0.15,
        }}
        keyboardShouldPersistTaps="handled"
        contentContainerStyle={{ paddingBottom: 16 }}
        ListHeaderComponent={
          <View style={{ padding: 20, paddingTop: 0, gap: 12 }}>
            {chat.more && (
              <Button
                title="Load earlier messages"
                busy={busy}
                disabled={!live}
                onPress={() => void act(() => chat.loadOlder())}
              />
            )}
          </View>
        }
        ListEmptyComponent={
          <EmptyState
            title={chat.resyncing ? "Loading conversation…" : "Make something happen"}
            detail="Send a prompt to your agents. Their work will appear here."
          />
        }
      />
      <View
        style={{
          padding: 16,
          paddingTop: 10,
          gap: 10,
          borderTopWidth: 1,
          borderTopColor: tokens.border,
        }}
      >
        <TextInput
          accessibilityLabel="Message to agents"
          value={text}
          onChangeText={setText}
          placeholder="What should we work on?"
          placeholderTextColor={tokens.muted}
          multiline
          maxLength={60_000}
          style={[styles.input, { maxHeight: 160, paddingTop: 14 }]}
          editable={!busy}
        />
        <Button
          title={
            online
              ? chat.resyncing
                ? "Syncing conversation…"
                : "Send message ↑"
              : "Queue prompt for later"
          }
          tone="primary"
          busy={busy}
          disabled={!text.trim() || (online && !live) || (!online && !onQueuePrompt)}
          onPress={() => void send()}
        />
      </View>
      <Modal
        visible={panel !== null}
        animationType="slide"
        presentationStyle="pageSheet"
        onRequestClose={() => setPanel(null)}
      >
        <View style={styles.screen} accessibilityViewIsModal>
          <ScreenHeader
            title={panel === "models" ? "Choose a model" : "Changes"}
            onBack={() => setPanel(null)}
            subtitle={
              panel === "changes"
                ? "Read-only summaries of your agent's work."
                : "Choose the agent, then its next model."
            }
          />
          <ScrollView contentContainerStyle={styles.content}>
            {panel === "models" ? (
              <>
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                  {[...new Set(chat.roster.map((agent) => agent.kind))].map((agentKind) => (
                    <Button
                      key={agentKind}
                      title={agentKind}
                      disabled={busy || !live}
                      tone={kind === agentKind ? "primary" : "default"}
                      onPress={() => void loadModels(agentKind)}
                    />
                  ))}
                </View>
                {!chat.roster.length && (
                  <Notice>No agents are attached to this conversation yet.</Notice>
                )}
                {choices.map((model) => (
                  <View key={model.id} style={styles.card}>
                    <Text style={styles.heading}>
                      {model.label}
                      {model.selected ? " · current" : ""}
                    </Text>
                    <Button
                      title="Use this model"
                      disabled={busy || !live}
                      onPress={() =>
                        void act(async () => {
                          await chat.setModel(kind, model.id);
                          setPanel(null);
                        })
                      }
                    />
                    {!!model.efforts?.length && (
                      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                        {model.efforts.map((effort) => (
                          <Button
                            key={effort}
                            title={effort}
                            disabled={busy || !live}
                            label={`Use ${model.label} with ${effort} effort`}
                            onPress={() =>
                              void act(async () => {
                                await chat.setModel(kind, model.id, effort);
                                setPanel(null);
                              })
                            }
                          />
                        ))}
                      </View>
                    )}
                  </View>
                ))}
              </>
            ) : chat.changes.length ? (
              chat.changes.map((file) => (
                <View key={file.path} style={styles.card}>
                  <Text selectable style={styles.code}>
                    {file.path}
                  </Text>
                  <Text style={{ color: tokens.success }}>
                    +{file.added} <Text style={{ color: tokens.danger }}>−{file.removed}</Text>
                  </Text>
                  {file.diff ? (
                    <Text selectable style={styles.code}>
                      {file.diff.slice(0, 32_000)}
                    </Text>
                  ) : (
                    <Text style={styles.secondary}>Diff preview not provided.</Text>
                  )}
                  {(file.diff_truncated || (file.diff?.length ?? 0) > 32_000) && (
                    <Text style={styles.secondary}>
                      Diff truncated. Open the computer to review it in full.
                    </Text>
                  )}
                </View>
              ))
            ) : (
              <EmptyState
                title="No changes yet"
                detail="File changes will appear as your agents work."
              />
            )}
            {error && <Notice error>{error}</Notice>}
          </ScrollView>
        </View>
      </Modal>
    </KeyboardAvoidingView>
  );
}
