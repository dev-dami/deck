import { FlashList } from "@shopify/flash-list";
import { memo, type ReactNode, useEffect, useRef, useState } from "react";
import { KeyboardAvoidingView, Platform, Pressable, Text, View } from "react-native";
import { type ChatSession, newIdempotencyKey } from "../../../../pkg/remote/web/src/chat3";
import { activeAgents, canSend, toggleAgent } from "../../../../pkg/remote/web/src/composer";
import type { Message } from "../../../../pkg/remote/web/src/protocol";
import { AgentRail } from "../ui/agents";
import { Button, Composer, EmptyState, IconButton, Notice, TopBar } from "../ui/components";
import { Icon } from "../ui/icons";
import { splitMessage } from "../ui/tasks";
import { mono, useTheme } from "../ui/theme";

export interface ChatScreenProps {
  chat: ChatSession;
  title: string;
  online: boolean;
  onBack(): void;
  onOpenChanges(): void;
  onOpenSession(): void;
  onAddAgent(): void;
  onQueuePrompt?(text: string): Promise<void>;
  // Pending approvals dock above the composer.
  docked?: ReactNode;
}

const MODE_LABEL = { ask: "Ask", plan: "Plan", yolo: "Yolo" } as const;

const MessageRow = memo(function MessageRow({
  message,
  showAgent,
}: {
  message: Message;
  showAgent: boolean;
}) {
  const { t, s } = useTheme();
  const [open, setOpen] = useState(false);
  if (message.role === "user") {
    const text = message.parts
      .map((p) => p.text ?? "")
      .join("\n")
      .trim();
    return (
      <View style={{ paddingHorizontal: 20, paddingVertical: 8, alignItems: "flex-end" }}>
        <View
          style={{
            maxWidth: "82%",
            borderRadius: 22,
            paddingHorizontal: 16,
            paddingVertical: 11,
            backgroundColor: t.chip,
          }}
        >
          <Text selectable style={{ fontSize: 16, lineHeight: 24, color: t.fg }}>
            {text || "Attachment"}
          </Text>
        </View>
      </View>
    );
  }
  const { prose, steps } = splitMessage(message.parts);
  return (
    <View style={{ paddingHorizontal: 20, paddingVertical: 8, gap: 10 }}>
      {showAgent && message.agent && (
        <Text style={[s.small, { fontWeight: "600", color: t.muted }]}>{message.agent}</Text>
      )}
      {steps.length > 0 && (
        <Pressable
          accessibilityRole="button"
          accessibilityState={{ expanded: open }}
          onPress={() => setOpen(!open)}
          style={{
            flexDirection: "row",
            alignItems: "center",
            gap: 6,
            minHeight: 30,
            alignSelf: "flex-start",
          }}
        >
          <Text style={s.secondary}>
            {message.finished ? "Worked" : "Working"} · {steps.length} step
            {steps.length === 1 ? "" : "s"}
            {steps.some((x) => x.error) ? " · 1+ failed" : ""}
          </Text>
          <View style={{ transform: [{ rotate: open ? "90deg" : "0deg" }] }}>
            <Icon name="chevron-right" color={t.muted} size={14} weight={2.2} />
          </View>
        </Pressable>
      )}
      {open && (
        <View style={{ gap: 8, paddingLeft: 14, borderLeftWidth: 2, borderLeftColor: t.line }}>
          {steps.map((step) => (
            <Text key={step.key} numberOfLines={2} style={{ fontSize: 14, color: t.soft }}>
              <Text style={{ color: step.error ? t.danger : t.muted }}>{step.label} </Text>
              <Text style={{ fontFamily: mono, fontSize: 13 }}>{step.detail}</Text>
            </Text>
          ))}
        </View>
      )}
      {prose.map((text, i) => (
        <Text key={`${message.id}:${i}`} selectable style={s.body}>
          {text.slice(0, 24_000)}
        </Text>
      ))}
      {!message.finished && prose.length === 0 && steps.length === 0 && (
        <Text accessibilityLiveRegion="polite" style={s.secondary}>
          Thinking…
        </Text>
      )}
    </View>
  );
});

export function ChatScreen({
  chat,
  title,
  online,
  onBack,
  onOpenChanges,
  onOpenSession,
  onAddAgent,
  onQueuePrompt,
  docked,
}: ChatScreenProps) {
  const { t, s } = useTheme();
  const [, tick] = useState(0);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const retrySend = useRef<{ text: string; key: string } | null>(null);
  const live = online && !chat.resyncing;
  useEffect(() => chat.subscribe(() => tick((n) => n + 1)), [chat]);

  async function act(action: () => Promise<unknown>): Promise<void> {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await action();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "The action failed.");
    } finally {
      setBusy(false);
    }
  }

  function send(): void {
    const value = text.trim();
    if (!value) return;
    void act(async () => {
      if (online) {
        if (!live) throw new Error("Wait for the conversation to finish syncing.");
        if (retrySend.current?.text !== value)
          retrySend.current = { text: value, key: newIdempotencyKey() };
        await chat.send(value, retrySend.current.key);
        retrySend.current = null;
      } else if (onQueuePrompt) await onQueuePrompt(value);
      else throw new Error("Reconnect to send this message.");
      setText("");
    });
  }

  const agents = new Set(chat.roster.map((a) => a.name));
  const addressed = activeAgents(text);
  const names = chat.roster.map((a) => a.name);
  const placeholder = !online
    ? "Queue a message for later"
    : names.length === 0
      ? "Message your agents"
      : addressed.length > 0
        ? `Message ${addressed.join(" and ")}`
        : `Message ${names.slice(0, 2).join(" or ")}${names.length > 2 ? "…" : ""}`;
  const model = chat.roster[0]?.model;
  const subtitle = [
    online ? null : "Offline",
    model ?? null,
    chat.mode ? MODE_LABEL[chat.mode] : null,
    agents.size > 1 ? `${agents.size} agents` : null,
  ]
    .filter(Boolean)
    .join(" · ");
  const added = chat.changes.reduce((n, f) => n + f.added, 0);
  const removed = chat.changes.reduce((n, f) => n + f.removed, 0);

  return (
    <KeyboardAvoidingView style={s.screen} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <TopBar
        title={title}
        subtitle={subtitle || undefined}
        left={<IconButton icon="back" label="Back to tasks" onPress={onBack} />}
        right={<IconButton icon="sliders" label="Session settings" onPress={onOpenSession} />}
      />
      {(error || chat.error) && (
        <View style={{ paddingHorizontal: 16, gap: 8 }}>
          <Notice error>{error || chat.error}</Notice>
          {chat.error && (
            <Button
              title="Retry sync"
              icon="refresh"
              size="sm"
              disabled={!online || chat.resyncing}
              onPress={() => void act(() => chat.refresh())}
            />
          )}
        </View>
      )}
      <FlashList
        data={chat.messages}
        keyExtractor={(m) => m.id}
        renderItem={({ item }) => <MessageRow message={item} showAgent={agents.size > 1} />}
        maintainVisibleContentPosition={{
          startRenderingFromBottom: true,
          autoscrollToBottomThreshold: 0.15,
        }}
        keyboardShouldPersistTaps="handled"
        contentContainerStyle={{ paddingVertical: 12 }}
        ListHeaderComponent={
          chat.more ? (
            <View style={{ padding: 16 }}>
              <Button
                title="Load earlier messages"
                size="sm"
                disabled={!live}
                onPress={() => void act(() => chat.loadOlder())}
              />
            </View>
          ) : null
        }
        ListEmptyComponent={
          <EmptyState
            title={chat.resyncing ? "Loading conversation…" : "Make something happen"}
            detail="Send a prompt to your agents. Their work appears here."
          />
        }
        ListFooterComponent={
          <View style={{ paddingHorizontal: 20, paddingTop: 4, gap: 12 }}>
            {chat.changes.length > 0 && (
              <Pressable
                accessibilityRole="button"
                accessibilityLabel={`${chat.changes.length} files changed, review`}
                onPress={onOpenChanges}
                style={[
                  s.card,
                  {
                    flexDirection: "row",
                    alignItems: "center",
                    gap: 12,
                    padding: 12,
                    paddingLeft: 14,
                    borderRadius: 18,
                  },
                ]}
              >
                <View
                  style={{
                    width: 36,
                    height: 36,
                    borderRadius: 11,
                    alignItems: "center",
                    justifyContent: "center",
                    backgroundColor: t.chip,
                  }}
                >
                  <Icon name="file" color={t.fg} size={17} />
                </View>
                <View style={{ flex: 1, gap: 2 }}>
                  <Text style={s.heading}>
                    {chat.changes.length} file{chat.changes.length === 1 ? "" : "s"} changed
                  </Text>
                  <Text style={{ fontFamily: mono, fontSize: 13 }}>
                    <Text style={{ color: t.success }}>+{added}</Text>{" "}
                    <Text style={{ color: t.danger }}>−{removed}</Text>
                  </Text>
                </View>
                <View
                  style={{
                    minHeight: 34,
                    paddingHorizontal: 14,
                    borderRadius: 17,
                    justifyContent: "center",
                    backgroundColor: t.chip,
                  }}
                >
                  <Text style={{ fontSize: 14, fontWeight: "600", color: t.fg }}>Review</Text>
                </View>
              </Pressable>
            )}
            {chat.busy && (
              <View
                accessibilityLiveRegion="polite"
                style={{ flexDirection: "row", alignItems: "center", gap: 10 }}
              >
                <View
                  style={{
                    width: 10,
                    height: 10,
                    borderRadius: 5,
                    backgroundColor: t.primary,
                    boxShadow: `0 0 0 4px ${t.tint(t.primary)}`,
                  }}
                />
                <Text style={s.secondary}>Working</Text>
              </View>
            )}
          </View>
        }
      />
      {docked && (
        <View style={{ paddingHorizontal: 12, paddingBottom: 10, gap: 10 }}>{docked}</View>
      )}
      <View style={{ paddingHorizontal: 12, paddingBottom: 16, gap: 10 }}>
        {online && (
          <AgentRail
            agents={chat.roster.map((a) => ({
              key: a.id,
              name: a.name,
              kind: a.kind,
              detail: [a.model ?? a.kind, a.busy ? "working" : null].filter(Boolean).join(" · "),
              on: addressed.includes(a.id) || addressed.includes(a.name),
              status: a.busy ? "busy" : "idle",
            }))}
            onToggle={(id) => setText(toggleAgent(text, id))}
            onAdd={onAddAgent}
          />
        )}
        <Composer
          value={text}
          onChangeText={setText}
          placeholder={placeholder}
          canSend={canSend(text, 0) && (online ? live : onQueuePrompt !== undefined)}
          busy={busy}
          onSend={send}
          onStop={online && chat.busy ? () => void act(() => chat.interrupt()) : undefined}
          sendLabel={online ? "Send message" : "Queue message"}
        />
      </View>
    </KeyboardAvoidingView>
  );
}
