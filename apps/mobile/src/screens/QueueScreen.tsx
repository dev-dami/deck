import { useState } from "react";
import { FlatList, Text, View } from "react-native";
import type { Receipt } from "../../../../pkg/remote/web/src/protocol3";
import { Button, EmptyState, Notice, ScreenHeader } from "../ui/components";
import { useTheme } from "../ui/ThemeContext";

export interface QueueRow {
  qid: string;
  kind: string;
  state: "outbox" | "sent";
  createdAt: string;
  expiresAt: string;
  label?: string;
}
export interface QueueScreenProps {
  rows: QueueRow[];
  receipts: Receipt[];
  onBack(): void;
  onRefresh(): Promise<void>;
  onCancel(qid: string): Promise<void>;
}
type Row = { type: "action"; item: QueueRow } | { type: "receipt"; item: Receipt };

export function QueueScreen({ rows, receipts, onBack, onRefresh, onCancel }: QueueScreenProps) {
  const { styles, tokens } = useTheme();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const data: Row[] = [
    ...rows.map((item): Row => ({ type: "action", item })),
    ...[...receipts]
      .sort((a, b) => b.at.localeCompare(a.at))
      .map((item): Row => ({ type: "receipt", item })),
  ];
  async function cancel(qid: string): Promise<void> {
    setBusy(qid);
    setError(null);
    try {
      await onCancel(qid);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "Could not cancel this action.");
    } finally {
      setBusy(null);
    }
  }
  return (
    <View style={styles.screen}>
      <ScreenHeader
        title="Waiting & receipts"
        subtitle="A clear record of what happens next."
        onBack={onBack}
      />
      <FlatList
        data={data}
        keyExtractor={(row) => `${row.type}:${row.item.qid}`}
        contentContainerStyle={styles.content}
        refreshing={refreshing}
        onRefresh={() => {
          setRefreshing(true);
          void onRefresh()
            .catch((failure) =>
              setError(failure instanceof Error ? failure.message : "Could not refresh."),
            )
            .finally(() => setRefreshing(false));
        }}
        ListHeaderComponent={
          <View style={{ gap: 12, marginBottom: 12 }}>
            <Notice>
              Actions expire if your computer cannot run them in time. Interrupt and yolo cannot be
              queued.
            </Notice>
            {error && <Notice error>{error}</Notice>}
          </View>
        }
        ListEmptyComponent={
          <EmptyState
            title="Nothing waiting"
            detail="Queued prompts and their delivery receipts will appear here."
          />
        }
        renderItem={({ item: row }) =>
          row.type === "action" ? (
            <View style={[styles.card, { marginBottom: 12 }]}>
              <Text style={styles.eyebrow}>
                {row.item.state === "outbox"
                  ? "On this phone · not delivered"
                  : "At relay · waiting for computer"}
              </Text>
              <Text style={styles.heading}>{row.item.label ?? row.item.kind}</Text>
              <Text style={styles.secondary}>
                Expires {new Date(row.item.expiresAt).toLocaleString()}
              </Text>
              <Button
                title={row.item.state === "outbox" ? "Remove from phone" : "Request cancellation"}
                busy={busy === row.item.qid}
                disabled={busy !== null}
                onPress={() => void cancel(row.item.qid)}
              />
            </View>
          ) : (
            <View style={[styles.card, { marginBottom: 12 }]}>
              <View style={styles.between}>
                <Text
                  style={[
                    styles.heading,
                    { color: row.item.status === "applied" ? tokens.success : tokens.warning },
                  ]}
                >
                  {row.item.status === "applied"
                    ? "Applied"
                    : row.item.status === "dropped"
                      ? "Dropped"
                      : "Failed"}
                </Text>
                <Text style={styles.secondary}>{new Date(row.item.at).toLocaleTimeString()}</Text>
              </View>
              {row.item.reason && (
                <Text style={styles.secondary}>{row.item.reason.replaceAll("_", " ")}</Text>
              )}
              <Text selectable style={styles.code}>
                {row.item.qid}
              </Text>
            </View>
          )
        }
      />
    </View>
  );
}
