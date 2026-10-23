import { useEffect, useRef, useState } from "react";
import { Modal, ScrollView, Text, View } from "react-native";
import type { ChatSession } from "../../../../pkg/remote/web/src/chat3";
import type { AgentLine, ModelChoice } from "../../../../pkg/remote/web/src/protocol";
import { agentMark, agentTint, type KindTones } from "../ui/agents";
import { Button, Chip, EmptyState, Notice, ScreenHeader } from "../ui/components";
import { useTheme } from "../ui/ThemeContext";

export interface AgentsSheetProps {
  chat: ChatSession;
  tones: KindTones;
  live: boolean;
  visible: boolean;
  onClose(): void;
}

// The desktop's agent roster and model picker: who is in the session, what
// each is doing, and which model each kind of agent uses next.
export function AgentsSheet({ chat, tones, live, visible, onClose }: AgentsSheetProps) {
  const { styles, tokens } = useTheme();
  const [kind, setKind] = useState<string | null>(null);
  const [choices, setChoices] = useState<ModelChoice[]>([]);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const request = useRef(0);
  useEffect(() => {
    if (!visible) {
      request.current++;
      setKind(null);
      setChoices([]);
      setError(null);
    }
  }, [visible]);

  async function pick(selected: string): Promise<void> {
    const mine = ++request.current;
    setKind(selected);
    setChoices([]);
    setError(null);
    setLoading(true);
    try {
      const models = await chat.models(selected);
      if (request.current === mine) setChoices(models);
    } catch (failure) {
      if (request.current === mine)
        setError(failure instanceof Error ? failure.message : "Could not load models.");
    } finally {
      if (request.current === mine) setLoading(false);
    }
  }

  async function act(work: () => Promise<void>): Promise<void> {
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

  const kinds = [...new Set(chat.roster.map((agent) => agent.kind))];
  return (
    <Modal
      visible={visible}
      animationType="slide"
      presentationStyle="pageSheet"
      onRequestClose={onClose}
    >
      <View style={styles.screen} accessibilityViewIsModal>
        <ScreenHeader
          title="Agents"
          subtitle="Who is working in this session, and on which model."
          onBack={onClose}
          action={
            chat.busy ? (
              <Button
                compact
                title="Interrupt"
                tone="danger"
                disabled={!live || busy}
                onPress={() => void act(() => chat.interrupt())}
              />
            ) : undefined
          }
        />
        <ScrollView contentContainerStyle={styles.content}>
          {!chat.roster.length && (
            <EmptyState
              title="No agents yet"
              detail="Agents join this session as you send prompts."
            />
          )}
          {chat.roster.map((agent: AgentLine) => {
            const tint = agentTint(tones, agent.id, agent.kind, tokens.text);
            return (
              <View
                key={agent.id}
                style={[styles.card, { borderLeftColor: tint, borderLeftWidth: 3 }]}
              >
                <View style={styles.between}>
                  <Text style={[styles.heading, { color: tint, flexShrink: 1 }]} numberOfLines={1}>
                    {agentMark(tones, agent.kind)} {agent.name}
                  </Text>
                  <Text
                    style={{
                      fontSize: 12,
                      color: agent.busy ? tokens.info : tokens.muted,
                      fontWeight: "600",
                    }}
                  >
                    {agent.busy ? "Working" : "Idle"}
                  </Text>
                </View>
                <Text style={styles.secondary}>
                  {agent.kind}
                  {agent.model ? ` · ${agent.model}` : ""}
                  {agent.effort ? ` · ${agent.effort}` : ""}
                </Text>
              </View>
            );
          })}
          {kinds.length > 0 && (
            <>
              <Text style={styles.eyebrow}>Model</Text>
              <Text style={styles.secondary}>
                A model change applies to every agent of that kind in this session.
              </Text>
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                {kinds.map((agentKind) => (
                  <Chip
                    key={agentKind}
                    title={agentKind}
                    active={kind === agentKind}
                    tint={agentTint(tones, agentKind, agentKind, tokens.text)}
                    onPress={live && !busy ? () => void pick(agentKind) : undefined}
                  />
                ))}
              </View>
            </>
          )}
          {loading && <Text style={styles.secondary}>Loading models…</Text>}
          {kind !== null && !loading && !choices.length && !error && (
            <Notice>No models to choose from.</Notice>
          )}
          {choices.map((model) => {
            const apply = (effort?: string): void =>
              void act(async () => {
                await chat.setModel(kind ?? "", model.id, effort);
                onClose();
              });
            return (
              <View key={model.id} style={styles.card}>
                <View style={styles.between}>
                  <Text style={[styles.heading, { flexShrink: 1 }]}>{model.label}</Text>
                  {model.selected && (
                    <Text style={{ color: tokens.success, fontSize: 12, fontWeight: "600" }}>
                      Current
                    </Text>
                  )}
                </View>
                <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
                  <Button
                    compact
                    title="Use this model"
                    tone={model.selected ? "default" : "primary"}
                    disabled={busy || !live}
                    onPress={() => apply()}
                  />
                  {model.efforts?.map((effort) => (
                    <Button
                      compact
                      key={effort}
                      title={effort}
                      disabled={busy || !live}
                      label={`Use ${model.label} with ${effort} effort`}
                      onPress={() => apply(effort)}
                    />
                  ))}
                </View>
              </View>
            );
          })}
          {error && <Notice error>{error}</Notice>}
        </ScrollView>
      </View>
    </Modal>
  );
}
