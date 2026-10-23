import { useRef, useState } from "react";
import { ScrollView, Text, View } from "react-native";
import type { PendingApproval, Snapshot } from "../../../../pkg/remote/web/src/protocol3";
import type { NativeClient } from "../core/client";
import { Button, Dot, Notice } from "../ui/components";
import { offlineApprovalInput } from "../ui/offline-approvals";
import { useTheme } from "../ui/theme";

export interface OfflineApprovalsProps {
  client: Pick<NativeClient, "queue">;
  cid: string;
  snapshot: Snapshot;
  onQueued?(): void;
}

function SavedApproval({
  client,
  cid,
  pending,
  onQueued,
}: Omit<OfflineApprovalsProps, "snapshot"> & { pending: PendingApproval }) {
  const { t, s } = useTheme();
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [answered, setAnswered] = useState(false);
  const locked = useRef(false);
  const { approval } = pending;
  const destructive = approval.tier !== "normal";
  const preview = approval.command || approval.diff || approval.raw || "";

  async function queue(decision: "allow" | "deny"): Promise<void> {
    if (locked.current || answered) return;
    locked.current = true;
    setBusy(true);
    setError(null);
    try {
      const result = await client.queue(cid, offlineApprovalInput(pending, decision, ""));
      setAnswered(true);
      setNotice(
        result.state === "sent"
          ? "Answer is at the relay. It only applies if this exact approval is still waiting."
          : "Answer saved on this phone. It hasn't reached the relay yet.",
      );
      onQueued?.();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "Could not queue this answer.");
    } finally {
      locked.current = false;
      setBusy(false);
    }
  }

  return (
    <View style={[s.card, { padding: 16, gap: 12 }]}>
      <View style={s.row}>
        <Dot color={destructive ? t.danger : t.warn} />
        <Text style={[s.heading, { flex: 1 }]} numberOfLines={2}>
          {approval.description || approval.kind}
        </Text>
        <Text style={s.small}>{approval.agent}</Text>
      </View>
      {approval.path && (
        <Text selectable style={s.code}>
          {approval.path}
        </Text>
      )}
      {preview !== "" && (
        <ScrollView nestedScrollEnabled style={{ maxHeight: 160 }}>
          <Text selectable style={s.code}>
            {preview.slice(0, 24_000)}
          </Text>
        </ScrollView>
      )}
      {answered ? (
        notice && <Text style={s.small}>{notice}</Text>
      ) : (
        <>
          <View style={{ flexDirection: "row", gap: 8 }}>
            <Button
              title="Queue denial"
              style={{ flex: 1 }}
              disabled={busy}
              onPress={() => void queue("deny")}
            />
            {!destructive && (
              <Button
                title="Approve when back"
                tone="primary"
                style={{ flex: 1.3 }}
                busy={busy}
                onPress={() => void queue("allow")}
              />
            )}
          </View>
          {destructive && <Text style={s.small}>Approving needs a live passkey check.</Text>}
        </>
      )}
      {error && <Text style={s.error}>{error}</Text>}
    </View>
  );
}

export function OfflineApprovals({ client, cid, snapshot, onQueued }: OfflineApprovalsProps) {
  const { s } = useTheme();
  if (!snapshot.approvals.length) return null;
  return (
    <View style={{ gap: 10 }}>
      <Text style={[s.label, { marginTop: 4 }]}>Saved approvals</Text>
      <Notice>
        These may have changed. Answers expire after 1 hour and only apply to this exact request.
      </Notice>
      {snapshot.approvals.map((pending) => (
        <SavedApproval
          key={`${cid}:${pending.pj}:${pending.approval.id}:${pending.approval.digest}`}
          client={client}
          cid={cid}
          pending={pending}
          onQueued={onQueued}
        />
      ))}
    </View>
  );
}
