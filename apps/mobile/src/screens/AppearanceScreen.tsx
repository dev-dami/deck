import { Pressable, ScrollView, Text, View } from "react-native";
import { Notice, ScreenHeader } from "../ui/components";
import { ACCENTS, resolveTokens, THEMES, themeInfo } from "../ui/palettes";
import { useTheme } from "../ui/ThemeContext";

// The desktop's Appearance settings: the same theme catalog and accents.
export function AppearanceScreen({ onBack }: { onBack(): void }) {
  const { styles, tokens, appearance, appearanceError, setAppearance } = useTheme();
  const system = tokens.mode;
  return (
    <View style={styles.screen}>
      <ScreenHeader
        title="Appearance"
        subtitle="Themes and accents match the desktop app."
        onBack={onBack}
      />
      <ScrollView contentContainerStyle={styles.content}>
        {appearanceError && <Notice error>{appearanceError}</Notice>}
        <Text style={styles.eyebrow}>Theme</Text>
        {THEMES.map((theme) => {
          const preview = resolveTokens({ theme: theme.id, accent: "theme" }, system);
          const selected = appearance.theme === theme.id;
          return (
            <Pressable
              key={theme.id}
              accessibilityRole="radio"
              accessibilityState={{ selected }}
              accessibilityLabel={`${theme.label}${theme.mode === null ? ", follows your phone" : `, ${theme.mode}`}`}
              onPress={() => setAppearance({ ...appearance, theme: theme.id })}
              style={[
                styles.card,
                { flexDirection: "row", alignItems: "center", gap: 14 },
                selected && { borderColor: tokens.accent },
              ]}
            >
              <View style={{ flexDirection: "row", borderRadius: 8, overflow: "hidden" }}>
                {[preview.background, preview.surface, preview.text, preview.accent].map(
                  (colour, index) => (
                    <View
                      // The four swatches are positional: background, surface, text, accent.
                      // biome-ignore lint/suspicious/noArrayIndexKey: fixed order, never reordered.
                      key={index}
                      style={{
                        width: 22,
                        height: 32,
                        backgroundColor: colour,
                        borderWidth: 0.5,
                        borderColor: tokens.border,
                      }}
                    />
                  ),
                )}
              </View>
              <View style={{ flex: 1 }}>
                <Text style={styles.heading}>{theme.label}</Text>
                <Text style={styles.secondary}>
                  {theme.mode === null
                    ? "Follows your phone"
                    : theme.mode === "dark"
                      ? "Dark"
                      : "Light"}
                </Text>
              </View>
              {selected && <Text style={{ color: tokens.accent, fontSize: 18 }}>✓</Text>}
            </Pressable>
          );
        })}
        <Text style={[styles.eyebrow, { paddingTop: 8 }]}>Accent</Text>
        <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 12 }}>
          {ACCENTS.map((accent) => {
            const colour = resolveTokens(
              { theme: appearance.theme, accent: accent.id },
              system,
            ).accent;
            const selected = appearance.accent === accent.id;
            return (
              <Pressable
                key={accent.id}
                accessibilityRole="radio"
                accessibilityState={{ selected }}
                accessibilityLabel={`${accent.label} accent`}
                onPress={() => setAppearance({ ...appearance, accent: accent.id })}
                style={{ alignItems: "center", gap: 6, minWidth: 64 }}
              >
                <View
                  style={{
                    width: 44,
                    height: 44,
                    borderRadius: 22,
                    backgroundColor: colour,
                    borderWidth: selected ? 3 : 1,
                    borderColor: selected ? tokens.text : tokens.border,
                  }}
                />
                <Text style={[styles.secondary, { fontSize: 12 }]}>{accent.label}</Text>
              </Pressable>
            );
          })}
        </View>
        <Text style={styles.secondary}>
          {themeInfo(appearance.theme).mode === null
            ? "The default theme switches between light and dark with your phone."
            : "This theme keeps its own light or dark look."}
        </Text>
      </ScrollView>
    </View>
  );
}
