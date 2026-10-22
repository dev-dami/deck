import { ACCENTS, type AppearancePrefs, DEFAULT_APPEARANCE, THEMES } from "../ui/palettes";

const KEY = "deck.mobile.appearance.v1";

// Stored text is untrusted: an unknown or damaged value falls back to the
// default rather than failing startup.
export function parseAppearance(raw: string | null): AppearancePrefs {
  if (raw === null) return DEFAULT_APPEARANCE;
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return DEFAULT_APPEARANCE;
  }
  if (typeof value !== "object" || value === null) return DEFAULT_APPEARANCE;
  const { theme, accent } = value as Record<string, unknown>;
  return {
    theme: THEMES.some((t) => t.id === theme) ? (theme as string) : DEFAULT_APPEARANCE.theme,
    accent: ACCENTS.some((a) => a.id === accent) ? (accent as string) : DEFAULT_APPEARANCE.accent,
  };
}

// Appearance is not a secret; SecureStore is already a dependency, so this
// avoids adding another native module just for two strings.
export async function loadAppearance(): Promise<AppearancePrefs> {
  const SecureStore = await import("expo-secure-store");
  return parseAppearance(await SecureStore.getItemAsync(KEY));
}

export async function saveAppearance(prefs: AppearancePrefs): Promise<void> {
  const SecureStore = await import("expo-secure-store");
  await SecureStore.setItemAsync(KEY, JSON.stringify(prefs));
}
