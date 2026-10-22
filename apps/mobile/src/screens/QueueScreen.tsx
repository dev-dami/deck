import { useState } from "react";
import { RefreshControl, ScrollView, Text, View } from "react-native";
import type { Receipt } from "../../../../pkg/remote/web/src/protocol3";
import {
  Button,
  Dot,
  EmptyState,
  Group,
  IconButton,
  LargeTitle,
  Notice,
  Row,
  TopBar,
} from "../ui/components";
import { useTheme } from "../ui/theme";

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

const KIND_LABEL: Record<string, string> = {
  "chat.send": "Send message",
  "session.new": "Start task",
  "chat.continue": "Continue",
  "models.set": "Change model",
  "approvals.answer": "Answer approval",
  "queue.cancel": "Cancel queued action",
};

export function QueueScreen({ rows, receipts, onBack, onRefresh, onCancel }: QueueScreenProps) {
  const { t, s } = useTheme();
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refreshing, setRefreshing] = useState(false);
  const sorted = [...receipts].sort((a, b) => b.at.localeCompare(a.at));

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

  const time = (iso: string) =>
    new Date(iso).toLocaleString([], {
      hour: "2-digit",
      minute: "2-digit",
      day: "numeric",
      month: "short",
    });

  return (
    <View style={s.screen}>
      <TopBar left={<IconButton icon="back" label="Back to tasks" onPress={onBack} />} />
      <ScrollView
        contentContainerStyle={{ paddingBottom: 40, gap: 10 }}
        refreshControl={
          <RefreshControl
            refreshing={refreshing}
            tintColor={t.primary}
            colors={[t.primary]}
            onRefresh={() => {
              setRefreshing(true);
              void onRefresh()
                .catch((failure: unknown) =>
                  setError(failure instanceof Error ? failure.message : "Could not refresh."),
                )
                .finally(() => setRefreshing(false));
            }}
          />
        }
      >
        <LargeTitle title="Activity" subtitle="What's waiting, and what happened." />
        <View style={{ paddingHorizontal: 16, paddingTop: 10, gap: 10 }}>
          {error && <Notice error>{error}</Notice>}
          <Text style={s.label}>Waiting · {rows.length}</Text>
          <Group>
            {rows.length === 0 ? (
              <EmptyState
                title="Nothing waiting"
                detail="Messages you queue while a computer is offline show up here."
              />
            ) : (
              rows.map((row, i) => (
                <Row
                  key={row.qid}
                  divider={i > 0}
                  title={row.label ?? KIND_LABEL[row.kind] ?? row.kind}
                  subtitle={`${row.state === "outbox" ? "On this phone" : "At the relay"} · expires ${time(row.expiresAt)}`}
                  leading={<Dot color={t.info} />}
                  trailing={
                    <Button
                      title={row.state === "outbox" ? "Remove" : "Cancel"}
                      size="sm"
                      busy={busy === row.qid}
                      disabled={busy !== null}
                      onPress={() => void cancel(row.qid)}
                    />
                  }
                />
              ))
            )}
          </Group>
          {sorted.length > 0 && (
            <>
              <Text style={[s.label, { marginTop: 14 }]}>Receipts</Text>
              <Group>
                {sorted.map((r, i) => (
                  <Row
                    key={r.qid}
                    divider={i > 0}
                    title={
                      r.status === "applied"
                        ? "Applied"
                        : r.status === "dropped"
                          ? "Dropped"
                          : "Failed"
                    }
                    subtitle={r.reason ? r.reason.replaceAll("_", " ") : r.qid}
                    leading={
                      <Dot
                        color={
                          r.status === "applied"
                            ? t.success
                            : r.status === "dropped"
                              ? t.warn
                              : t.danger
                        }
                      />
                    }
                    trailing={<Text style={s.small}>{time(r.at)}</Text>}
                  />
                ))}
              </Group>
            </>
          )}
          <Text style={[s.small, { marginHorizontal: 6, marginTop: 8 }]}>
            Queued actions expire if your computer can't run them in time. Interrupt and yolo can't
            be queued.
          </Text>
        </View>
      </ScrollView>
    </View>
  );
}
