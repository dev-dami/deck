import { FlatList, Pressable, Text, View } from "react-native";
import type { ComputerView } from "../../../../pkg/remote/web/src/state3";
import { Button, EmptyState, ScreenHeader } from "../ui/components";
import { useTheme } from "../ui/ThemeContext";

export interface ComputersScreenProps {
  computers: ComputerView[];
  onSelect(cid: string): void;
  onPair(): void;
  onRetry(cid: string): void;
  onAppearance(): void;
}

export function ComputersScreen({
  computers,
  onSelect,
  onPair,
  onRetry,
  onAppearance,
}: ComputersScreenProps) {
  const { styles, tokens } = useTheme();
  return (
    <View style={styles.screen}>
      <ScreenHeader
        title="Your computers"
        subtitle="A little closer to your work, wherever you are."
        action={
          <View style={styles.row}>
            <Button compact title="Appearance" onPress={onAppearance} />
            <Button compact title="+ Pair" tone="primary" onPress={onPair} />
          </View>
        }
      />
      <FlatList
        data={computers}
        keyExtractor={(item) => item.record.cid}
        contentContainerStyle={styles.content}
        ListEmptyComponent={
          <EmptyState
            title="Bring your desk with you"
            detail="Run deck mobile pair on your computer, then scan its QR code to connect securely."
          />
        }
        ListFooterComponent={
          <Text style={[styles.secondary, { paddingTop: 12 }]}>
            Your conversations stay encrypted between this phone and your computer.
          </Text>
        }
        renderItem={({ item }) => {
          const online = item.status === "online";
          const needsYou = item.inbox.filter((s) => s.state === "needs_you").length;
          const running = item.inbox.filter((s) => s.state === "running").length;
          return (
            <View style={[styles.card, { marginBottom: 12 }]}>
              <Pressable
                accessibilityRole="button"
                accessibilityLabel={`Open ${item.record.name}, ${item.status}`}
                onPress={() => onSelect(item.record.cid)}
                style={{ gap: 12, minHeight: 80 }}
              >
                <View style={styles.between}>
                  <Text style={styles.heading}>{item.record.name}</Text>
                  <Text style={{ color: online ? tokens.success : tokens.muted, fontSize: 12 }}>
                    {online
                      ? "● Connected"
                      : item.status === "connecting"
                        ? "◌ Connecting"
                        : item.status === "revoked"
                          ? "○ Revoked"
                          : item.status === "needs-pairing"
                            ? "○ Pairing needed"
                            : "○ Offline"}
                  </Text>
                </View>
                <Text style={styles.secondary}>
                  {needsYou
                    ? `${needsYou} need your attention`
                    : running
                      ? `${running} running`
                      : online
                        ? "All caught up"
                        : item.lastSeen
                          ? `Last seen ${new Date(item.lastSeen).toLocaleString()}`
                          : "Waiting for a connection"}
                </Text>
              </Pressable>
              {item.error && <Text style={styles.error}>{item.error}</Text>}
              {item.status !== "online" && item.status !== "connecting" && (
                <Button
                  title={
                    item.status === "revoked" || item.status === "needs-pairing"
                      ? "Pair again"
                      : "Reconnect"
                  }
                  onPress={() =>
                    item.status === "revoked" || item.status === "needs-pairing"
                      ? onPair()
                      : onRetry(item.record.cid)
                  }
                />
              )}
            </View>
          );
        }}
      />
    </View>
  );
}
