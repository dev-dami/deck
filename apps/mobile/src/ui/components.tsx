import type { ReactNode } from "react";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { useTheme } from "./ThemeContext";

export function Button({
  title,
  onPress,
  disabled = false,
  busy = false,
  tone = "default",
  label,
  compact = false,
}: {
  title: string;
  onPress(): void;
  disabled?: boolean;
  busy?: boolean;
  // "ghost" is the desktop's quiet decline: no fill, no border.
  tone?: "default" | "primary" | "danger" | "ghost";
  label?: string;
  compact?: boolean;
}) {
  const { tokens } = useTheme();
  const blocked = disabled || busy;
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label ?? title}
      accessibilityState={{ disabled: blocked, busy }}
      disabled={blocked}
      onPress={onPress}
      style={({ pressed }) => ({
        minHeight: compact ? 40 : 48,
        borderRadius: 12,
        paddingVertical: compact ? 8 : 12,
        paddingHorizontal: compact ? 12 : 16,
        alignItems: "center",
        justifyContent: "center",
        flexDirection: "row",
        gap: 8,
        borderWidth: 1,
        borderColor:
          tone === "primary" ? tokens.accent : tone === "ghost" ? "transparent" : tokens.border,
        backgroundColor:
          tone === "primary" ? tokens.accent : tone === "ghost" ? "transparent" : tokens.elevated,
        opacity: blocked ? 0.45 : pressed ? 0.72 : 1,
      })}
    >
      {busy && (
        <ActivityIndicator
          size="small"
          color={tone === "primary" ? tokens.accentInk : tokens.text}
        />
      )}
      <Text
        style={{
          fontSize: 14,
          fontWeight: "600",
          color:
            tone === "primary"
              ? tokens.accentInk
              : tone === "danger"
                ? tokens.danger
                : tone === "ghost"
                  ? tokens.secondary
                  : tokens.text,
        }}
      >
        {title}
      </Text>
    </Pressable>
  );
}

export function ScreenHeader({
  title,
  subtitle,
  onBack,
  action,
}: {
  title: string;
  subtitle?: string;
  onBack?: () => void;
  action?: ReactNode;
}) {
  const { styles } = useTheme();
  return (
    <View style={{ padding: 20, paddingBottom: 16, gap: 12 }}>
      {onBack && (
        <View style={{ alignSelf: "flex-start" }}>
          <Button title="‹ Back" onPress={onBack} compact />
        </View>
      )}
      <View style={styles.between}>
        <Text accessibilityRole="header" style={[styles.title, { flex: 1 }]}>
          {title}
        </Text>
        {action}
      </View>
      {subtitle && <Text style={styles.secondary}>{subtitle}</Text>}
    </View>
  );
}

export function Notice({ children, error = false }: { children: ReactNode; error?: boolean }) {
  const { styles, tokens } = useTheme();
  return (
    <View
      accessibilityLiveRegion="polite"
      style={{
        padding: 14,
        borderRadius: 12,
        borderWidth: 1,
        borderColor: tokens.border,
        backgroundColor: tokens.surface,
      }}
    >
      <Text style={error ? styles.error : styles.secondary}>{children}</Text>
    </View>
  );
}

export function EmptyState({ title, detail }: { title: string; detail: string }) {
  const { styles } = useTheme();
  return (
    <View style={{ padding: 24, paddingVertical: 44, gap: 10, alignItems: "center" }}>
      <Text style={[styles.heading, { textAlign: "center" }]}>{title}</Text>
      <Text style={[styles.secondary, { textAlign: "center", maxWidth: 320 }]}>{detail}</Text>
    </View>
  );
}

// A thread's state at a glance: a pulsing-free dot, since the list must stay
// cheap to scroll. Colours follow the desktop sidebar.
export function StatusDot({ status }: { status: "approval" | "working" | "done" | null }) {
  const { tokens } = useTheme();
  if (status === null) return null;
  const colour =
    status === "approval" ? tokens.warning : status === "working" ? tokens.info : tokens.success;
  return (
    <View
      accessibilityElementsHidden
      importantForAccessibility="no"
      style={{ width: 8, height: 8, borderRadius: 4, backgroundColor: colour }}
    />
  );
}

export function Chip({
  title,
  active = false,
  onPress,
  tint,
  busy = false,
  label,
}: {
  title: string;
  active?: boolean;
  onPress?(): void;
  tint?: string;
  busy?: boolean;
  label?: string;
}) {
  const { tokens } = useTheme();
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label ?? title}
      accessibilityState={{ selected: active, busy }}
      disabled={!onPress}
      onPress={onPress}
      style={({ pressed }) => ({
        flexDirection: "row",
        alignItems: "center",
        gap: 6,
        minHeight: 36,
        paddingHorizontal: 12,
        borderRadius: 18,
        borderWidth: 1,
        borderColor: active ? tokens.accent : tokens.border,
        backgroundColor: active ? tokens.elevated : "transparent",
        opacity: pressed ? 0.72 : 1,
      })}
    >
      {busy && (
        <View style={{ width: 6, height: 6, borderRadius: 3, backgroundColor: tokens.info }} />
      )}
      <Text style={{ fontSize: 13, fontWeight: "600", color: tint ?? tokens.text }}>{title}</Text>
    </Pressable>
  );
}
