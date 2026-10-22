import { FlashList } from "@shopify/flash-list";
import { memo, type ReactNode, useEffect, useRef, useState } from "react";
import {
  ActivityIndicator,
  KeyboardAvoidingView,
  Modal,
  Platform,
  Pressable,
  ScrollView,
  Text,
  TextInput,
  View,
} from "react-native";
import { type ChatSession, newIdempotencyKey } from "../../../../pkg/remote/web/src/chat3";
import type { ChangedFile, Message } from "../../../../pkg/remote/web/src/protocol";
import { agentMark, agentTint, type KindTones } from "../ui/agents";
import { Button, Chip, EmptyState, Notice, ScreenHeader } from "../ui/components";
import { DiffView } from "../ui/DiffView";
import { useTheme } from "../ui/ThemeContext";
import { AgentsSheet } from "./AgentsSheet";

export interface ChatScreenProps {
  chat: ChatSession;
  online: boolean;
  // The computer's per-kind tints and marks, from its welcome theme.
  tones: KindTones;
  onBack(): void;
  onOpenQueue(): void;
  onQueuePrompt?(text: string): Promise<void>;
  onOpenSession?(sessionId: string): void;
  children?: ReactNode;
}

const PREVIEW_LIMIT = 24_000;

const MessageRow = memo(function MessageRow({
  message,
  tones,
}: {
  message: Message;
  tones: KindTones;
}) {
  const { styles, tokens } = useTheme();
  const own = message.role === "user";
  const occurrences = new Map<string, number>();
  const kind = message.agent ? message.agent.replace(/[-_]?\d+$/, "") : "";
  const tint = own ? tokens.accent : agentTint(tones, message.agent ?? "", kind, tokens.secondary);
  return (
    <View
      style={{
        paddingHorizontal: 16,
        paddingVertical: 8,
        alignItems: own ? "flex-end" : "stretch",
      }}
    >
      <View
        style={
          own
            ? {
                maxWidth: "88%",
                padding: 12,
                gap: 8,
                borderRadius: 18,
                backgroundColor: tokens.elevated,
              }
            : { gap: 8 }
        }
      >
        {!own && (
          <View style={styles.row}>
            <Text style={{ color: tint, fontSize: 12, fontWeight: "700" }}>
              {agentMark(tones, kind)} {message.agent || message.role}
            </Text>
            <Text style={[styles.secondary, { fontSize: 11 }]}>
              {new Date(message.created_at).toLocaleTimeString([], {
                hour: "2-digit",
                minute: "2-digit",
              })}
            </Text>
          </View>
        )}
        {message.parts.map((part) => {
          const identity = `${part.type}:${part.id ?? part.tool_call_id ?? part.name ?? part.image?.name ?? "body"}`;
          const occurrence = occurrences.get(identity) ?? 0;
          occurrences.set(identity, occurrence + 1);
          const key = `${identity}:${occurrence}`;
          if (part.diff)
            return (
              <View key={key} style={{ gap: 4 }}>
                {part.name && <Text style={styles.eyebrow}>{part.name}</Text>}
                <DiffView
                  diff={part.diff.slice(0, PREVIEW_LIMIT)}
                  truncated={part.diff_truncated || part.diff.length > PREVIEW_LIMIT}
                />
              </View>
            );
          const text = part.text ?? part.output ?? part.summary;
          if (text !== undefined)
            return (
              <View key={key} style={{ gap: 4 }}>
                {part.name && <Text style={styles.eyebrow}>{part.name}</Text>}
                {part.type === "text" ? (
                  <Text selectable style={styles.body}>
                    {text.slice(0, PREVIEW_LIMIT)}
                  </Text>
                ) : (
                  <View
                    style={{
                      padding: 10,
                      borderRadius: 10,
                      borderWidth: 1,
                      borderColor: tokens.border,
                      backgroundColor: tokens.code,
                    }}
                  >
                    <Text
                      selectable
                      style={[styles.code, part.is_error ? { color: tokens.danger } : {}]}
                    >
                      {text.slice(0, PREVIEW_LIMIT)}
                    </Text>
                  </View>
                )}
                {(part.text_truncated || part.output_truncated || text.length > PREVIEW_LIMIT) && (
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

function ChangeCard({ file }: { file: ChangedFile }) {
  const { styles, tokens } = useTheme();
  const [open, setOpen] = useState(false);
  const hasDiff = !!file.diff;
  return (
    <View style={[styles.card, { padding: 0, overflow: "hidden" }]}>
      <Pressable
        accessibilityRole="button"
        accessibilityState={{ expanded: open, disabled: !hasDiff }}
        accessibilityLabel={`${file.path}, ${file.added} added, ${file.removed} removed`}
        disabled={!hasDiff}
        onPress={() => setOpen(!open)}
        style={[styles.between, { padding: 14, minHeight: 52 }]}
      >
        <Text selectable style={[styles.code, { flex: 1 }]} numberOfLines={2}>
          {file.path}
        </Text>
        <Text style={{ fontSize: 12, fontWeight: "600" }}>
          <Text style={{ color: tokens.diffAddFg }}>+{file.added}</Text>{" "}
          <Text style={{ color: tokens.diffDelFg }}>−{file.removed}</Text>
        </Text>
      </Pressable>
      {open && file.diff && (
        <View style={{ padding: 10, paddingTop: 0 }}>
          <DiffView
            diff={file.diff.slice(0, 32_000)}
            truncated={file.diff_truncated || file.diff.length > 32_000}
          />
        </View>
      )}
      {!hasDiff && (
        <Text style={[styles.secondary, { paddingHorizontal: 14, paddingBottom: 12 }]}>
          Diff preview not provided.
        </Text>
      )}
    </View>
  );
}

export function ChatScreen({
  chat,
  online,
  tones,
  onBack,
  onOpenQueue,
  onQueuePrompt,
  onOpenSession,
  children,
}: ChatScreenProps) {
  const { styles, tokens } = useTheme();
  const [, tick] = useState(0);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [panel, setPanel] = useState<"agents" | "changes" | null>(null);
  const retrySend = useRef<{ text: string; key: string } | null>(null);
  const acting = useRef(false);
  const identity = `${chat.pj}\0${chat.sessionId}`;
  const draftIdentity = useRef(identity);
  const live = online && !chat.resyncing;
  useEffect(() => {
    return chat.subscribe(() => tick((value) => value + 1));
  }, [chat]);
  useEffect(() => {
    if (draftIdentity.current === identity) return;
    draftIdentity.current = identity;
    setText("");
    setError(null);
    retrySend.current = null;
  }, [identity]);
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
  const added = chat.changes.reduce((n, file) => n + file.added, 0);
  const removed = chat.changes.reduce((n, file) => n + file.removed, 0);
  const canSend = !!text.trim() && !(online && !live) && !(!online && !onQueuePrompt);
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
        contentContainerStyle={{ paddingHorizontal: 20, gap: 8, paddingBottom: 8 }}
      >
        <Chip title="Agents" active={panel === "agents"} onPress={() => setPanel("agents")} />
        {chat.roster.map((agent) => (
          <Chip
            key={agent.id}
            title={`${agentMark(tones, agent.kind)} ${agent.name}`}
            label={`${agent.name}, ${agent.busy ? "working" : "idle"}`}
            tint={agentTint(tones, agent.id, agent.kind, tokens.text)}
            busy={agent.busy}
            onPress={() => setPanel("agents")}
          />
        ))}
      </ScrollView>
      <ScrollView
        horizontal
        showsHorizontalScrollIndicator={false}
        style={{ flexGrow: 0 }}
        contentContainerStyle={{ paddingHorizontal: 20, gap: 8, paddingBottom: 12 }}
      >
        <Button
          compact
          title={chat.busy ? "Interrupt" : "Continue"}
          tone={chat.busy ? "danger" : "default"}
          disabled={!live || busy}
          onPress={() => void act(() => (chat.busy ? chat.interrupt() : chat.cont()))}
        />
        <Button
          compact
          title={
            chat.changes.length
              ? `Changes · ${chat.changes.length} file${chat.changes.length === 1 ? "" : "s"}`
              : "Changes"
          }
          onPress={() => {
            setPanel("changes");
            if (live) void act(() => chat.refreshChanges());
          }}
        />
        <Button
          compact
          title="New session"
          disabled={!live || busy || !onOpenSession}
          onPress={() =>
            void act(async () => {
              const id = await chat.createSession();
              onOpenSession?.(id);
            })
          }
        />
        <Button compact title="Queue" onPress={onOpenQueue} />
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
          style={{ flexGrow: 0, maxHeight: "40%" }}
          contentContainerStyle={{ paddingHorizontal: 16, paddingBottom: 12, gap: 12 }}
        >
          {children}
        </ScrollView>
      )}
      <FlashList
        data={chat.messages}
        keyExtractor={(message) => message.id}
        renderItem={({ item }) => <MessageRow message={item} tones={tones} />}
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
      <View style={{ paddingHorizontal: 12, paddingBottom: 12, paddingTop: 6 }}>
        <View
          style={{
            flexDirection: "row",
            alignItems: "flex-end",
            gap: 8,
            padding: 8,
            paddingLeft: 14,
            borderRadius: 24,
            borderWidth: 1,
            borderColor: tokens.input,
            backgroundColor: tokens.surface,
          }}
        >
          <TextInput
            accessibilityLabel="Message to agents"
            value={text}
            onChangeText={setText}
            placeholder={online ? "What should we work on?" : "Queue a prompt for later"}
            placeholderTextColor={tokens.muted}
            multiline
            maxLength={60_000}
            style={{
              flex: 1,
              color: tokens.text,
              fontSize: 16,
              maxHeight: 160,
              minHeight: 40,
              paddingTop: 9,
              paddingBottom: 9,
            }}
            editable={!busy}
          />
          <Pressable
            accessibilityRole="button"
            accessibilityLabel={
              online
                ? chat.resyncing
                  ? "Syncing conversation"
                  : "Send message"
                : "Queue prompt for later"
            }
            accessibilityState={{ disabled: !canSend || busy, busy }}
            disabled={!canSend || busy}
            onPress={() => void send()}
            style={{
              width: 40,
              height: 40,
              borderRadius: 20,
              alignItems: "center",
              justifyContent: "center",
              backgroundColor: tokens.accent,
              opacity: !canSend || busy ? 0.4 : 1,
            }}
          >
            {busy ? (
              <ActivityIndicator size="small" color={tokens.accentInk} />
            ) : (
              <Text style={{ color: tokens.accentInk, fontSize: 20, fontWeight: "700" }}>
                {online ? "↑" : "⏲"}
              </Text>
            )}
          </Pressable>
        </View>
      </View>
      <AgentsSheet
        chat={chat}
        tones={tones}
        live={live}
        visible={panel === "agents"}
        onClose={() => setPanel(null)}
      />
      <Modal
        visible={panel === "changes"}
        animationType="slide"
        presentationStyle="pageSheet"
        onRequestClose={() => setPanel(null)}
      >
        <View style={styles.screen} accessibilityViewIsModal>
          <ScreenHeader
            title="Changes"
            onBack={() => setPanel(null)}
            subtitle={
              chat.changes.length
                ? `${chat.changes.length} file${chat.changes.length === 1 ? "" : "s"}  +${added}  −${removed}`
                : "Read-only summaries of your agent's work."
            }
            action={
              live ? (
                <Button
                  compact
                  title="Refresh"
                  disabled={busy}
                  onPress={() => void act(() => chat.refreshChanges())}
                />
              ) : undefined
            }
          />
          <ScrollView contentContainerStyle={styles.content}>
            {chat.changes.length ? (
              chat.changes.map((file) => <ChangeCard key={file.path} file={file} />)
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
