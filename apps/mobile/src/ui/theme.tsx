import * as SecureStore from "expo-secure-store";
import { createContext, type ReactNode, useContext, useEffect, useMemo, useState } from "react";
import { Platform, StyleSheet, useColorScheme } from "react-native";
import {
  buildTheme,
  DEFAULT_DARK,
  DEFAULT_LIGHT,
  isThemeId,
  palette,
  type Theme,
  type ThemeId,
} from "./themes";

export type { Theme, ThemeId } from "./themes";

const THEME_KEY = "deck.theme";

export const mono = Platform.select({ ios: "Menlo", android: "monospace", default: "monospace" });

export function makeStyles(t: Theme) {
  return StyleSheet.create({
    screen: { flex: 1, backgroundColor: t.bg },
    content: { padding: 16, paddingBottom: 40, gap: 12 },
    title: { fontSize: 30, lineHeight: 36, fontWeight: "700", color: t.fg, letterSpacing: -0.8 },
    sheetTitle: { fontSize: 20, fontWeight: "700", color: t.fg, letterSpacing: -0.3 },
    heading: { fontSize: 16, fontWeight: "600", color: t.fg },
    body: { color: t.soft, fontSize: 16, lineHeight: 26 },
    secondary: { color: t.muted, fontSize: 14, lineHeight: 20 },
    small: { color: t.muted, fontSize: 13, lineHeight: 18 },
    label: { color: t.muted, fontSize: 13, fontWeight: "600", marginHorizontal: 4 },
    card: { backgroundColor: t.card, borderRadius: 22, boxShadow: t.shadow.card },
    float: { backgroundColor: t.card, borderRadius: 26, boxShadow: t.shadow.float },
    row: { flexDirection: "row", alignItems: "center", gap: 12 },
    between: {
      flexDirection: "row",
      justifyContent: "space-between",
      alignItems: "center",
      gap: 12,
    },
    divider: { borderTopWidth: StyleSheet.hairlineWidth * 2, borderTopColor: t.line },
    input: {
      color: t.fg,
      backgroundColor: t.chip,
      borderRadius: 14,
      paddingHorizontal: 14,
      paddingVertical: 12,
      fontSize: 16,
      minHeight: 48,
    },
    code: {
      fontFamily: mono,
      fontSize: 13,
      lineHeight: 20,
      color: t.soft,
      backgroundColor: t.code,
      borderRadius: 12,
      paddingHorizontal: 12,
      paddingVertical: 10,
      boxShadow: `inset 0 0 0 1px ${t.line}`,
    },
    error: { color: t.danger, fontSize: 14, lineHeight: 20 },
  });
}

export type Styles = ReturnType<typeof makeStyles>;

interface ThemeValue {
  t: Theme;
  s: Styles;
  themeId: ThemeId;
  setTheme(id: ThemeId): void;
}

const ThemeContext = createContext<ThemeValue | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const scheme = useColorScheme();
  const [chosen, setChosen] = useState<ThemeId | null>(null);
  useEffect(() => {
    let active = true;
    void SecureStore.getItemAsync(THEME_KEY)
      .then((saved) => {
        if (active && isThemeId(saved)) setChosen(saved);
      })
      // Without a saved choice the system light or dark setting decides.
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, []);
  const themeId = chosen ?? (scheme === "light" ? DEFAULT_LIGHT : DEFAULT_DARK);
  const value = useMemo<ThemeValue>(() => {
    const t = buildTheme(palette(themeId));
    return {
      t,
      s: makeStyles(t),
      themeId,
      setTheme: (id) => {
        setChosen(id);
        // The choice still applies for this run if the OS store refuses it.
        void SecureStore.setItemAsync(THEME_KEY, id).catch(() => undefined);
      },
    };
  }, [themeId]);
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): ThemeValue {
  const value = useContext(ThemeContext);
  if (value === null) throw new Error("useTheme needs a ThemeProvider.");
  return value;
}
