import { Pressable, ScrollView, Text, View } from "react-native";
import { Icon } from "./icons";
import { mono, useTheme } from "./theme";

// Each agent CLI's mark and colour, as the CLI and desktop app draw them.
const KINDS: Record<string, { mark: string; color: string }> = {
  claude: { mark: "✳", color: "#E08A68" },
  codex: { mark: ">_", color: "#C9CAD0" },
  opencode: { mark: "▯", color: "#9FB7C9" },
  antigravity: { mark: "◎", color: "#A78BFA" },
};

export function kindStyle(kind: string): { mark: string; color: string } {
  return KINDS[kind] ?? { mark: "●", color: "#9AA5A2" };
}

export interface AgentCardData {
  key: string;
  name: string;
  kind: string;
  detail: string;
  on: boolean;
  status?: "busy" | "idle";
}

export function AgentCard({
  agent,
  onPress,
  onLongPress,
}: {
  agent: AgentCardData;
  onPress(): void;
  onLongPress?: () => void;
}) {
  const { t } = useTheme();
  const k = kindStyle(agent.kind);
  return (
    <Pressable
      accessibilityRole="checkbox"
      accessibilityState={{ checked: agent.on }}
      accessibilityLabel={`${agent.name}, ${agent.detail}${agent.on ? ", addressed" : ""}`}
      accessibilityHint={onLongPress ? "Long press to choose its model" : undefined}
      onPress={onPress}
      onLongPress={onLongPress}
      style={({ pressed }) => ({
        flexDirection: "row",
        alignItems: "center",
        gap: 9,
        minWidth: 140,
        minHeight: 48,
        paddingVertical: 7,
        paddingLeft: 8,
        paddingRight: 12,
        borderRadius: 14,
        backgroundColor: agent.on ? t.thumb : t.card,
        boxShadow: agent.on
          ? `inset 0 0 0 1.5px ${k.color}, ${t.shadow.card}`
          : `inset 0 0 0 1px ${t.line}`,
        opacity: pressed ? 0.8 : 1,
      })}
    >
      <View
        style={{
          width: 28,
          height: 28,
          borderRadius: 9,
          alignItems: "center",
          justifyContent: "center",
          backgroundColor: t.tint(k.color),
        }}
      >
        <Text style={{ color: k.color, fontSize: 13, fontWeight: "700", fontFamily: mono }}>
          {k.mark}
        </Text>
      </View>
      <View style={{ flexShrink: 1 }}>
        <Text numberOfLines={1} style={{ fontSize: 14, fontWeight: "600", color: t.fg }}>
          {agent.name}
        </Text>
        <Text numberOfLines={1} style={{ fontSize: 12, color: t.muted }}>
          {agent.detail}
        </Text>
      </View>
      {agent.status && (
        <View
          style={{
            width: 7,
            height: 7,
            borderRadius: 4,
            marginLeft: "auto",
            backgroundColor: agent.status === "busy" ? t.info : t.success,
          }}
        />
      )}
    </Pressable>
  );
}

// The agents a message can go to, each in its own section, and adding
// another as an action of its own.
export function AgentRail({
  agents,
  onToggle,
  onLongPress,
  onAdd,
  addLabel = "Add agent",
}: {
  agents: AgentCardData[];
  onToggle(key: string): void;
  onLongPress?: (key: string) => void;
  onAdd?: () => void;
  addLabel?: string;
}) {
  const { t } = useTheme();
  return (
    <ScrollView
      horizontal
      showsHorizontalScrollIndicator={false}
      keyboardShouldPersistTaps="handled"
      contentContainerStyle={{ gap: 8, paddingVertical: 2, paddingHorizontal: 2 }}
    >
      {agents.map((agent) => (
        <AgentCard
          key={agent.key}
          agent={agent}
          onPress={() => onToggle(agent.key)}
          onLongPress={onLongPress ? () => onLongPress(agent.key) : undefined}
        />
      ))}
      {onAdd && (
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={addLabel}
          onPress={onAdd}
          style={({ pressed }) => ({
            flexDirection: "row",
            alignItems: "center",
            gap: 6,
            minHeight: 48,
            paddingHorizontal: 14,
            borderRadius: 14,
            borderWidth: 1,
            borderStyle: "dashed",
            borderColor: t.line,
            opacity: pressed ? 0.7 : 1,
          })}
        >
          <Icon name="plus" color={t.muted} size={16} weight={2.2} />
          <Text style={{ fontSize: 13, fontWeight: "500", color: t.muted }}>{addLabel}</Text>
        </Pressable>
      )}
    </ScrollView>
  );
}
