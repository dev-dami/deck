import {
  createContext,
  type ReactNode,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from "react";
import { useColorScheme } from "react-native";
import { loadAppearance, saveAppearance } from "../storage/prefs";
import { type AppearancePrefs, DEFAULT_APPEARANCE, resolveTokens, type Tokens } from "./palettes";
import { createStyles, type Styles } from "./theme";

interface Theme {
  tokens: Tokens;
  styles: Styles;
  appearance: AppearancePrefs;
  // Set when the saved choice could not be read or written.
  appearanceError: string | null;
  setAppearance(next: AppearancePrefs): void;
}

const ThemeContext = createContext<Theme | null>(null);

export function ThemeProvider({ children }: { children: ReactNode }) {
  const system = useColorScheme() === "light" ? "light" : "dark";
  const [appearance, setPrefs] = useState<AppearancePrefs>(DEFAULT_APPEARANCE);
  const [appearanceError, setError] = useState<string | null>(null);
  useEffect(() => {
    let active = true;
    void loadAppearance()
      .then((saved) => {
        if (active) setPrefs(saved);
      })
      .catch(() => {
        if (active) setError("Your saved appearance could not be read, so the default is shown.");
      });
    return () => {
      active = false;
    };
  }, []);
  const setAppearance = useCallback((next: AppearancePrefs) => {
    setPrefs(next);
    setError(null);
    void saveAppearance(next).catch(() =>
      setError("Your appearance could not be saved and will reset when Deck restarts."),
    );
  }, []);
  const tokens = useMemo(() => resolveTokens(appearance, system), [appearance, system]);
  const styles = useMemo(() => createStyles(tokens), [tokens]);
  const value = useMemo(
    () => ({ tokens, styles, appearance, appearanceError, setAppearance }),
    [tokens, styles, appearance, appearanceError, setAppearance],
  );
  return <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>;
}

export function useTheme(): Theme {
  const theme = useContext(ThemeContext);
  if (theme === null) throw new Error("useTheme needs a ThemeProvider.");
  return theme;
}
