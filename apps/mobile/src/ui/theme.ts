import { Platform, StyleSheet } from "react-native";
import type { Tokens } from "./palettes";

export const mono = Platform.select({ ios: "Menlo", android: "monospace", default: "monospace" });

// Styles are built from the active palette; useTheme() memoizes them.
export function createStyles(tokens: Tokens) {
  return StyleSheet.create({
    screen: { flex: 1, backgroundColor: tokens.background },
    content: { padding: 20, paddingBottom: 40, gap: 16 },
    title: { fontSize: 28, fontWeight: "700", color: tokens.text, letterSpacing: -0.6 },
    heading: { fontSize: 16, fontWeight: "600", color: tokens.text },
    body: { color: tokens.text, fontSize: 15, lineHeight: 23 },
    secondary: { color: tokens.secondary, fontSize: 14, lineHeight: 21 },
    eyebrow: {
      color: tokens.muted,
      fontSize: 11,
      fontWeight: "700",
      letterSpacing: 1.2,
      textTransform: "uppercase",
    },
    card: {
      backgroundColor: tokens.surface,
      borderColor: tokens.border,
      borderWidth: 1,
      borderRadius: 16,
      padding: 16,
      gap: 10,
    },
    row: { flexDirection: "row", alignItems: "center", gap: 12 },
    between: {
      flexDirection: "row",
      justifyContent: "space-between",
      alignItems: "center",
      gap: 12,
    },
    input: {
      color: tokens.text,
      backgroundColor: tokens.background,
      borderWidth: 1,
      borderColor: tokens.input,
      borderRadius: 12,
      padding: 14,
      fontSize: 16,
      minHeight: 48,
    },
    code: { fontFamily: mono, fontSize: 12.5, lineHeight: 19, color: tokens.text },
    error: { color: tokens.danger, fontSize: 14, lineHeight: 21 },
  });
}

export type Styles = ReturnType<typeof createStyles>;
