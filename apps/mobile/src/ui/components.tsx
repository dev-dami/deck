import type { ReactNode } from "react";
import { ActivityIndicator, Pressable, Text, View } from "react-native";
import { styles, tokens } from "./theme";

export function Button({
  title,
  onPress,
  disabled = false,
  busy = false,
  tone = "default",
  label,
}: {
  title: string;
  onPress(): void;
  disabled?: boolean;
  busy?: boolean;
  tone?: "default" | "primary" | "danger";
  label?: string;
}) {
  const blocked = disabled || busy;
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label ?? title}
      accessibilityState={{ disabled: blocked, busy }}
      disabled={blocked}
      onPress={onPress}
      style={({ pressed }) => ({
        minHeight: 48,
        borderRadius: 12,
        paddingVertical: 12,
        paddingHorizontal: 16,
        alignItems: "center",
        justifyContent: "center",
        flexDirection: "row",
        gap: 8,
        borderWidth: 1,
        borderColor: tone === "primary" ? tokens.accent : tokens.border,
        backgroundColor: tone === "primary" ? tokens.accent : tokens.elevated,
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
            tone === "primary" ? tokens.accentInk : tone === "danger" ? tokens.danger : tokens.text,
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
  return (
    <View style={{ padding: 20, paddingBottom: 16, gap: 12 }}>
      {onBack && (
        <View style={{ alignSelf: "flex-start" }}>
          <Button title="‹ Back" onPress={onBack} />
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
  return (
    <View style={{ padding: 24, paddingVertical: 44, gap: 10, alignItems: "center" }}>
      <Text style={[styles.heading, { textAlign: "center" }]}>{title}</Text>
      <Text style={[styles.secondary, { textAlign: "center", maxWidth: 320 }]}>{detail}</Text>
    </View>
  );
}
