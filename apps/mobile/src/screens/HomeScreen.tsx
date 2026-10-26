import { type ReactNode, useEffect, useState } from "react";
import { KeyboardAvoidingView, Platform, Pressable, ScrollView, Text, View } from "react-native";
import type { ModelChoice } from "../../../../pkg/remote/web/src/protocol";
import type { ComputerView } from "../../../../pkg/remote/web/src/state3";
import { AgentRail } from "../ui/agents";
import {
  Badge,
  Button,
  Chip,
  Composer,
  Dot,
  EmptyState,
  Group,
  IconButton,
  Notice,
  Row,
} from "../ui/components";
import { Icon } from "../ui/icons";
import { defaultTab, type TaskTab, tabCounts, taskRows } from "../ui/tasks";
import { mono, useTheme } from "../ui/theme";
import { ModelPicker } from "./ModelPicker";

export interface AgentPick {
  kind: string;
  model?: string;
  label?: string;
}

export interface HomeScreenProps {
  computer: ComputerView | null;
  waiting: boolean;
  onOpenTask(pj: string, sessionId: string): void;
  onStartTask(pj: string, text: string, agents: AgentPick[]): Promise<void>;
  // The agent CLIs installed on the computer.
  kinds: readonly string[];
  listModels(pj: string, kind: string): Promise<ModelChoice[]>;
  onOpenComputers(): void;
  onOpenActivity(): void;
  onOpenSettings(): void;
  onPair(): void;
  onRetry(): void;
  children?: ReactNode;
}

const TABS: ReadonlyArray<{ value: TaskTab; label: string }> = [
  { value: "needs", label: "Needs you" },
  { value: "running", label: "Running" },
  { value: "done", label: "Done" },
];

const EMPTY: Record<TaskTab, [string, string]> = {
  needs: ["All caught up", "Nothing needs your answer right now."],
  running: ["Nothing running", "Start a task above."],
  done: ["No finished tasks yet", "Finished work lands here."],
};

function statusOf(computer: ComputerView): string {
  switch (computer.status) {
    case "online":
      return "Connected";
    case "connecting":
      return "Connecting";
    case "revoked":
      return "Access revoked";
    case "needs-pairing":
      return "Pair again";
    default:
      return computer.lastSeen
        ? `Offline since ${new Date(computer.lastSeen).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" })}`
        : "Offline";
  }
}

export function HomeScreen({
  computer,
  waiting,
  onOpenTask,
  onStartTask,
  kinds,
  listModels,
  onOpenComputers,
  onOpenActivity,
  onOpenSettings,
  onPair,
  onRetry,
  children,
}: HomeScreenProps) {
  const { t, s } = useTheme();
  const items = computer?.inbox ?? [];
  const [tab, setTab] = useState<TaskTab | null>(null);
  const [draft, setDraft] = useState("");
  const [project, setProject] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [picks, setPicks] = useState<AgentPick[]>([]);
  const [choosing, setChoosing] = useState<string | null>(null);
  const cid = computer?.record.cid;
  // A different computer has its own tasks and projects.
  useEffect(() => {
    void cid;
    setTab(null);
    setProject(null);
    setPicks([]);
  }, [cid]);

  if (computer === null)
    return (
      <View style={[s.screen, { justifyContent: "center", padding: 24 }]}>
        <EmptyState
          title="Bring your desk with you"
          detail="Run deck mobile pair on your computer, then scan the code it shows."
          action={<Button title="Pair a computer" tone="primary" size="lg" onPress={onPair} />}
        />
      </View>
    );

  const online = computer.status === "online";
  const active = tab ?? defaultTab(items);
  const counts = tabCounts(items);
  const rows = taskRows(items, computer.projects, active, Date.now());
  const projects = computer.projects;
  const chosen = projects.find((p) => p.id === project) ?? projects[0];
  const dot =
    computer.status === "online" ? t.success : computer.status === "connecting" ? t.warn : t.muted;

  const start = (): void => {
    const text = draft.trim();
    if (!text || !chosen) return;
    setBusy(true);
    setError(null);
    void onStartTask(chosen.id, text, picks)
      .then(() => {
        setDraft("");
        setPicks([]);
      })
      .catch((err: unknown) =>
        setError(err instanceof Error ? err.message : "Could not start the task."),
      )
      .finally(() => setBusy(false));
  };

  return (
    <KeyboardAvoidingView style={s.screen} behavior={Platform.OS === "ios" ? "padding" : undefined}>
      <View
        style={{
          flexDirection: "row",
          alignItems: "center",
          gap: 4,
          paddingLeft: 16,
          paddingRight: 8,
          paddingVertical: 6,
        }}
      >
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={`${computer.record.name}, ${statusOf(computer)}. Switch computer`}
          onPress={onOpenComputers}
          style={{ flex: 1, flexDirection: "row", alignItems: "center", gap: 10, minHeight: 48 }}
        >
          <View
            style={[
              s.card,
              {
                width: 38,
                height: 38,
                borderRadius: 12,
                alignItems: "center",
                justifyContent: "center",
              },
            ]}
          >
            <Icon name="laptop" color={t.fg} size={19} weight={1.9} />
            <View
              style={{
                position: "absolute",
                right: -3,
                bottom: -3,
                borderWidth: 2.5,
                borderColor: t.bg,
                borderRadius: 7,
              }}
            >
              <Dot color={dot} />
            </View>
          </View>
          <View style={{ flex: 1, minWidth: 0 }}>
            <View style={{ flexDirection: "row", alignItems: "center", gap: 4 }}>
              <Text
                numberOfLines={1}
                style={{ fontSize: 16, fontWeight: "600", color: t.fg, flexShrink: 1 }}
              >
                {computer.record.name}
              </Text>
              <Icon name="chevron-down" color={t.muted} size={15} weight={2.2} />
            </View>
            <Text style={s.small}>{statusOf(computer)}</Text>
          </View>
        </Pressable>
        <IconButton
          icon="clock"
          label="Activity"
          onPress={onOpenActivity}
          dot={waiting ? t.info : undefined}
        />
        <IconButton icon="sliders" label="Settings" onPress={onOpenSettings} />
      </View>

      <ScrollView
        contentContainerStyle={{ padding: 16, paddingTop: 10, paddingBottom: 40, gap: 12 }}
        keyboardShouldPersistTaps="handled"
      >
        {online && kinds.length > 0 && (
          <View style={{ gap: 8 }}>
            <Text style={s.label}>Agents for this task</Text>
            <AgentRail
              agents={kinds.map((kind) => {
                const pick = picks.find((p) => p.kind === kind);
                return {
                  key: kind,
                  name: kind,
                  kind,
                  detail: pick ? (pick.label ?? "default model") : "tap to add",
                  on: pick !== undefined,
                };
              })}
              onToggle={(kind) =>
                setPicks((now) =>
                  now.some((p) => p.kind === kind)
                    ? now.filter((p) => p.kind !== kind)
                    : [...now, { kind }],
                )
              }
              onLongPress={(kind) => setChoosing(kind)}
            />
            {picks.length > 0 && (
              <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 6 }}>
                {picks.map((p) => (
                  <Chip
                    key={p.kind}
                    title={`${p.kind} · ${p.label ?? "default model"}`}
                    trailing="chevron-down"
                    label={`Choose the model for ${p.kind}`}
                    onPress={() => setChoosing(p.kind)}
                  />
                ))}
              </View>
            )}
            {picks.length === 0 && (
              <Text style={[s.small, { marginHorizontal: 4 }]}>
                None picked: your computer's default agent takes the task.
              </Text>
            )}
          </View>
        )}
        {online ? (
          <Composer
            tall
            value={draft}
            onChangeText={setDraft}
            placeholder="What should we work on?"
            canSend={draft.trim() !== "" && chosen !== undefined}
            busy={busy}
            onSend={start}
            sendLabel="Start task"
            footer={
              chosen ? (
                <Chip
                  title={chosen.name}
                  icon="folder"
                  trailing={projects.length > 1 ? "chevron-down" : undefined}
                  label={`Project ${chosen.name}${projects.length > 1 ? ", change" : ""}`}
                  onPress={
                    projects.length > 1
                      ? () => {
                          const at = projects.indexOf(chosen);
                          setProject(projects[(at + 1) % projects.length]?.id ?? null);
                        }
                      : undefined
                  }
                />
              ) : (
                <Text style={s.small}>Share a project on your computer to start tasks.</Text>
              )
            }
          />
        ) : (
          <View style={[s.card, { padding: 16, gap: 12 }]}>
            <View style={{ flexDirection: "row", gap: 12, alignItems: "flex-start" }}>
              <Icon name="offline" color={t.muted} size={18} />
              <Text style={[s.secondary, { flex: 1, color: t.soft }]}>
                {computer.status === "connecting"
                  ? "Connecting to your computer…"
                  : "You're seeing saved activity. Messages you queue wait until this computer is back."}
              </Text>
            </View>
            {computer.status !== "connecting" &&
              (computer.status === "revoked" || computer.status === "needs-pairing" ? (
                <Button title="Pair again" onPress={onPair} />
              ) : (
                <Button title="Reconnect" icon="refresh" onPress={onRetry} />
              ))}
          </View>
        )}
        {error && <Notice error>{error}</Notice>}
        {computer.error && <Notice error>{computer.error}</Notice>}
        {children}

        <View
          accessibilityRole="tablist"
          style={{
            flexDirection: "row",
            gap: 4,
            marginTop: 6,
            borderBottomWidth: 1,
            borderBottomColor: t.line,
          }}
        >
          {TABS.map((option) => {
            const on = option.value === active;
            return (
              <Pressable
                key={option.value}
                accessibilityRole="tab"
                accessibilityState={{ selected: on }}
                onPress={() => setTab(option.value)}
                style={{
                  minHeight: 44,
                  paddingHorizontal: 10,
                  flexDirection: "row",
                  alignItems: "center",
                  gap: 6,
                  marginBottom: -1,
                  borderBottomWidth: 2,
                  borderBottomColor: on ? t.primary : "transparent",
                }}
              >
                <Text style={{ fontSize: 14, fontWeight: "500", color: on ? t.fg : t.muted }}>
                  {option.label}
                </Text>
                <Text style={{ fontSize: 12, fontFamily: mono, color: t.muted }}>
                  {counts[option.value]}
                </Text>
              </Pressable>
            );
          })}
        </View>

        <Group>
          {rows.length === 0 ? (
            <EmptyState title={EMPTY[active][0]} detail={EMPTY[active][1]} />
          ) : (
            rows.map((row, i) => (
              <Row
                key={row.key}
                divider={i > 0}
                title={row.title}
                subtitle={row.subtitle}
                label={`${row.title}${row.badge ? `, ${row.badge.text}` : ""}${row.unread ? ", unread" : ""}`}
                onPress={() => onOpenTask(row.pj, row.sessionId)}
                leading={
                  <View style={{ width: 20, alignItems: "center" }}>
                    {active === "needs" ? (
                      <Icon name="shield" color={t.warn} size={18} weight={1.8} />
                    ) : active === "done" ? (
                      <Icon name="check-circle" color={t.success} size={18} weight={1.8} />
                    ) : (
                      <View
                        style={{
                          width: 9,
                          height: 9,
                          borderRadius: 5,
                          backgroundColor: t.primary,
                          boxShadow: `0 0 0 4px ${t.tint(t.primary)}`,
                        }}
                      />
                    )}
                  </View>
                }
                trailing={
                  row.badge ? (
                    <Badge
                      text={row.badge.text}
                      color={row.badge.tone === "warn" ? t.warn : t.info}
                    />
                  ) : undefined
                }
              />
            ))
          )}
        </Group>
      </ScrollView>
      {chosen && (
        <ModelPicker
          visible={choosing !== null}
          kind={choosing ?? ""}
          selected={picks.find((p) => p.kind === choosing)?.model}
          load={() => listModels(chosen.id, choosing ?? "")}
          onClose={() => setChoosing(null)}
          onPick={(model) => {
            const kind = choosing;
            if (kind === null) return;
            setPicks((now) => [
              ...now.filter((p) => p.kind !== kind),
              { kind, model: model?.id, label: model?.label },
            ]);
            setChoosing(null);
          }}
        />
      )}
    </KeyboardAvoidingView>
  );
}
