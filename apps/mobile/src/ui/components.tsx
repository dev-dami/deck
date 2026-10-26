import type { ReactNode } from "react";
import {
  ActivityIndicator,
  Modal,
  Pressable,
  type StyleProp,
  Text,
  TextInput,
  View,
  type ViewStyle,
} from "react-native";
import { useSafeAreaInsets } from "react-native-safe-area-context";
import { Icon, type IconName } from "./icons";
import { useTheme } from "./theme";

type Tone = "primary" | "secondary" | "danger" | "ghost";

export function Button({
  title,
  onPress,
  tone = "secondary",
  size = "md",
  icon,
  disabled = false,
  busy = false,
  label,
  style,
}: {
  title: string;
  onPress(): void;
  tone?: Tone;
  size?: "sm" | "md" | "lg";
  icon?: IconName;
  disabled?: boolean;
  busy?: boolean;
  label?: string;
  style?: StyleProp<ViewStyle>;
}) {
  const { t } = useTheme();
  const blocked = disabled || busy;
  const height = size === "lg" ? 52 : size === "sm" ? 36 : 46;
  const fill =
    tone === "primary"
      ? t.action
      : tone === "danger"
        ? t.tint(t.danger)
        : tone === "ghost"
          ? "transparent"
          : t.chip;
  const ink =
    tone === "primary"
      ? t.onAction
      : tone === "danger"
        ? t.danger
        : tone === "ghost"
          ? t.muted
          : t.fg;
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label ?? title}
      accessibilityState={{ disabled: blocked, busy }}
      disabled={blocked}
      onPress={onPress}
      style={({ pressed }) => [
        {
          minHeight: height,
          borderRadius: size === "lg" ? 10 : 8,
          paddingHorizontal: size === "sm" ? 14 : 18,
          alignItems: "center",
          justifyContent: "center",
          flexDirection: "row",
          gap: 8,
          backgroundColor: fill,
          boxShadow: tone === "secondary" ? t.shadow.card : undefined,
          opacity: blocked ? 0.45 : pressed ? 0.75 : 1,
        },
        style,
      ]}
    >
      {busy ? (
        <ActivityIndicator size="small" color={ink} />
      ) : (
        icon && <Icon name={icon} color={ink} size={size === "sm" ? 15 : 18} />
      )}
      <Text style={{ fontSize: size === "sm" ? 13 : 15, fontWeight: "600", color: ink }}>
        {title}
      </Text>
    </Pressable>
  );
}

export function IconButton({
  icon,
  label,
  onPress,
  color,
  dot,
  disabled = false,
}: {
  icon: IconName;
  label: string;
  onPress(): void;
  color?: string;
  dot?: string;
  disabled?: boolean;
}) {
  const { t } = useTheme();
  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityState={{ disabled }}
      disabled={disabled}
      onPress={onPress}
      hitSlop={4}
      style={({ pressed }) => ({
        width: 44,
        height: 44,
        borderRadius: 10,
        alignItems: "center",
        justifyContent: "center",
        backgroundColor: pressed ? t.chip : "transparent",
        opacity: disabled ? 0.4 : 1,
      })}
    >
      <Icon name={icon} color={color ?? t.fg} size={21} weight={1.9} />
      {dot && (
        <View
          style={{
            position: "absolute",
            top: 9,
            right: 9,
            width: 10,
            height: 10,
            borderRadius: 5,
            backgroundColor: dot,
            borderWidth: 2,
            borderColor: t.bg,
          }}
        />
      )}
    </Pressable>
  );
}

export function TopBar({
  title,
  subtitle,
  left,
  right,
}: {
  title?: string;
  subtitle?: ReactNode;
  left?: ReactNode;
  right?: ReactNode;
}) {
  const { t } = useTheme();
  return (
    <View
      style={{
        flexDirection: "row",
        alignItems: "center",
        gap: 4,
        paddingHorizontal: 8,
        minHeight: 56,
      }}
    >
      <View style={{ width: 44 }}>{left}</View>
      <View style={{ flex: 1, alignItems: "center", gap: 1 }}>
        {title !== undefined && (
          <Text
            accessibilityRole="header"
            numberOfLines={1}
            style={{ fontSize: 16, fontWeight: "600", color: t.fg }}
          >
            {title}
          </Text>
        )}
        {typeof subtitle === "string" ? (
          <Text numberOfLines={1} style={{ fontSize: 12, color: t.muted }}>
            {subtitle}
          </Text>
        ) : (
          subtitle
        )}
      </View>
      <View style={{ width: 44, alignItems: "flex-end" }}>{right}</View>
    </View>
  );
}

export function LargeTitle({ title, subtitle }: { title: string; subtitle?: string }) {
  const { s } = useTheme();
  return (
    <View style={{ paddingHorizontal: 20, paddingBottom: 4, gap: 4 }}>
      <Text accessibilityRole="header" style={s.title}>
        {title}
      </Text>
      {subtitle && <Text style={[s.secondary, { fontSize: 15 }]}>{subtitle}</Text>}
    </View>
  );
}

export function Chip({
  title,
  onPress,
  icon,
  trailing,
  label,
}: {
  title: string;
  onPress?: () => void;
  icon?: IconName;
  trailing?: IconName;
  label?: string;
}) {
  const { t } = useTheme();
  return (
    <Pressable
      accessibilityRole={onPress ? "button" : "text"}
      accessibilityLabel={label ?? title}
      disabled={!onPress}
      onPress={onPress}
      style={({ pressed }) => ({
        minHeight: 36,
        borderRadius: 18,
        paddingHorizontal: 12,
        flexDirection: "row",
        alignItems: "center",
        gap: 6,
        backgroundColor: t.card,
        boxShadow: t.shadow.card,
        opacity: pressed ? 0.75 : 1,
      })}
    >
      {icon && <Icon name={icon} color={t.fg} size={14} />}
      <Text style={{ fontSize: 13, fontWeight: "500", color: t.fg }}>{title}</Text>
      {trailing && <Icon name={trailing} color={t.muted} size={13} weight={2.2} />}
    </Pressable>
  );
}

export function Segmented<T extends string>({
  options,
  value,
  onChange,
  label,
  disabled = false,
}: {
  options: ReadonlyArray<{ value: T; label: string }>;
  value: T | null;
  onChange(value: T): void;
  label: string;
  disabled?: boolean;
}) {
  const { t } = useTheme();
  return (
    <View
      accessibilityRole="radiogroup"
      accessibilityLabel={label}
      style={{
        flexDirection: "row",
        padding: 3,
        borderRadius: 11,
        backgroundColor: t.track,
        boxShadow: `inset 0 0 0 1px ${t.line}`,
      }}
    >
      {options.map((option) => {
        const on = option.value === value;
        return (
          <Pressable
            key={option.value}
            accessibilityRole="radio"
            accessibilityState={{ checked: on, disabled }}
            disabled={disabled}
            onPress={() => onChange(option.value)}
            style={{
              flex: 1,
              minHeight: 40,
              borderRadius: 8,
              alignItems: "center",
              justifyContent: "center",
              backgroundColor: on ? t.thumb : "transparent",
              boxShadow: on ? t.shadow.thumb : undefined,
              opacity: disabled ? 0.5 : 1,
            }}
          >
            <Text style={{ fontSize: 14, fontWeight: "600", color: on ? t.fg : t.muted }}>
              {option.label}
            </Text>
          </Pressable>
        );
      })}
    </View>
  );
}

export function Sheet({
  visible,
  onClose,
  title,
  subtitle,
  children,
  done = true,
}: {
  visible: boolean;
  onClose(): void;
  title: string;
  subtitle?: string;
  children: ReactNode;
  done?: boolean;
}) {
  const { t, s } = useTheme();
  const insets = useSafeAreaInsets();
  return (
    <Modal
      visible={visible}
      transparent
      animationType="slide"
      statusBarTranslucent
      navigationBarTranslucent
      onRequestClose={onClose}
    >
      <View style={{ flex: 1, backgroundColor: t.scrim }}>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Close"
          onPress={onClose}
          style={{ flex: 1, minHeight: 60 }}
        />
        <View
          accessibilityViewIsModal
          style={{
            maxHeight: "88%",
            backgroundColor: t.sheet,
            borderTopLeftRadius: 20,
            borderTopRightRadius: 20,
            paddingTop: 8,
            paddingHorizontal: 18,
            paddingBottom: Math.max(insets.bottom, 16) + 12,
            gap: 14,
            boxShadow: t.shadow.sheet,
          }}
        >
          <View
            style={{
              alignSelf: "center",
              width: 38,
              height: 5,
              borderRadius: 3,
              backgroundColor: t.line,
            }}
          />
          <View style={[s.between, { paddingHorizontal: 4 }]}>
            <View style={{ flex: 1, gap: 2 }}>
              <Text accessibilityRole="header" style={s.sheetTitle}>
                {title}
              </Text>
              {subtitle && <Text style={s.small}>{subtitle}</Text>}
            </View>
            {done && <Button title="Done" size="sm" onPress={onClose} />}
          </View>
          {children}
        </View>
      </View>
    </Modal>
  );
}

export function Composer({
  value,
  onChangeText,
  placeholder,
  onSend,
  canSend,
  busy = false,
  onStop,
  footer,
  tall = false,
  editable = true,
  sendLabel = "Send message",
}: {
  value: string;
  onChangeText(text: string): void;
  placeholder: string;
  onSend(): void;
  canSend: boolean;
  busy?: boolean;
  onStop?: () => void;
  footer?: ReactNode;
  tall?: boolean;
  editable?: boolean;
  sendLabel?: string;
}) {
  const { t, s } = useTheme();
  const stop = onStop !== undefined && !value.trim();
  const action = stop ? (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel="Stop the agent"
      onPress={onStop}
      style={{
        width: 40,
        height: 40,
        borderRadius: 20,
        alignItems: "center",
        justifyContent: "center",
        backgroundColor: t.fg,
      }}
    >
      <View style={{ width: 12, height: 12, borderRadius: 3, backgroundColor: t.bg }} />
    </Pressable>
  ) : (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={sendLabel}
      accessibilityState={{ disabled: !canSend || busy, busy }}
      disabled={!canSend || busy}
      onPress={onSend}
      style={{
        width: 40,
        height: 40,
        borderRadius: 20,
        alignItems: "center",
        justifyContent: "center",
        backgroundColor: canSend ? t.primary : t.chip,
        boxShadow: canSend ? t.shadow.primary : undefined,
      }}
    >
      {busy ? (
        <ActivityIndicator size="small" color={t.onPrimary} />
      ) : (
        <Icon name="send" color={canSend ? t.onPrimary : t.muted} size={18} weight={2.4} />
      )}
    </Pressable>
  );
  const input = (
    <TextInput
      accessibilityLabel={placeholder}
      value={value}
      onChangeText={onChangeText}
      placeholder={placeholder}
      placeholderTextColor={t.muted}
      multiline
      maxLength={60_000}
      editable={editable && !busy}
      style={{
        flex: tall ? undefined : 1,
        color: t.fg,
        fontSize: tall ? 17 : 16,
        lineHeight: tall ? 25 : 22,
        minHeight: tall ? 50 : 24,
        maxHeight: 140,
        paddingTop: 0,
        paddingBottom: 0,
        textAlignVertical: "top",
      }}
    />
  );
  if (tall || footer)
    return (
      <View style={[s.float, { padding: 14, paddingLeft: 18, paddingRight: 12, gap: 14 }]}>
        {input}
        <View style={{ flexDirection: "row", alignItems: "center", gap: 6, marginLeft: -6 }}>
          {footer}
          <View style={{ flex: 1 }} />
          {action}
        </View>
      </View>
    );
  return (
    <View
      style={[
        s.float,
        { flexDirection: "row", alignItems: "center", gap: 8, padding: 7, paddingLeft: 18 },
      ]}
    >
      {input}
      {action}
    </View>
  );
}

export function Dot({ color, size = 8 }: { color: string; size?: number }) {
  return (
    <View style={{ width: size, height: size, borderRadius: size / 2, backgroundColor: color }} />
  );
}

export function Badge({ text, color }: { text: string; color: string }) {
  const { t } = useTheme();
  return (
    <View
      style={{
        paddingHorizontal: 10,
        paddingVertical: 3,
        borderRadius: 6,
        backgroundColor: t.tint(color),
      }}
    >
      <Text style={{ fontSize: 12, fontWeight: "600", color }}>{text}</Text>
    </View>
  );
}

export function Notice({ children, error = false }: { children: ReactNode; error?: boolean }) {
  const { t, s } = useTheme();
  return (
    <View
      accessibilityLiveRegion="polite"
      style={[
        s.card,
        {
          borderRadius: 10,
          paddingVertical: 12,
          paddingHorizontal: 16,
          flexDirection: "row",
          gap: 10,
          alignItems: "flex-start",
        },
      ]}
    >
      {error && (
        <View style={{ marginTop: 6 }}>
          <Dot color={t.danger} />
        </View>
      )}
      <Text style={[error ? s.error : s.secondary, { flex: 1 }]}>{children}</Text>
    </View>
  );
}

export function EmptyState({
  title,
  detail,
  action,
}: {
  title: string;
  detail: string;
  action?: ReactNode;
}) {
  const { s } = useTheme();
  return (
    <View style={{ paddingHorizontal: 24, paddingVertical: 32, gap: 8, alignItems: "center" }}>
      <Text style={[s.heading, { textAlign: "center" }]}>{title}</Text>
      <Text style={[s.secondary, { textAlign: "center", maxWidth: 300 }]}>{detail}</Text>
      {action && <View style={{ marginTop: 8 }}>{action}</View>}
    </View>
  );
}

export function Group({ children }: { children: ReactNode }) {
  const { s } = useTheme();
  return <View style={[s.card, { overflow: "hidden" }]}>{children}</View>;
}

export function Row({
  title,
  subtitle,
  onPress,
  leading,
  trailing,
  divider = false,
  label,
}: {
  title: string;
  subtitle?: string;
  onPress?: () => void;
  leading?: ReactNode;
  trailing?: ReactNode;
  divider?: boolean;
  label?: string;
}) {
  const { t, s } = useTheme();
  return (
    <Pressable
      accessibilityRole={onPress ? "button" : undefined}
      accessibilityLabel={label}
      disabled={!onPress}
      onPress={onPress}
      style={({ pressed }) => [
        {
          flexDirection: "row",
          alignItems: "center",
          gap: 12,
          minHeight: 56,
          paddingHorizontal: 16,
          paddingVertical: 12,
          backgroundColor: pressed ? t.chip : "transparent",
        },
        divider && s.divider,
      ]}
    >
      {leading}
      <View style={{ flex: 1, minWidth: 0, gap: 3 }}>
        <Text numberOfLines={1} style={{ fontSize: 15, fontWeight: "600", color: t.fg }}>
          {title}
        </Text>
        {subtitle && (
          <Text numberOfLines={2} style={s.small}>
            {subtitle}
          </Text>
        )}
      </View>
      {trailing}
    </Pressable>
  );
}
