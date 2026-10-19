import { useRef, useState } from "react";
import { ScrollView, Text, TextInput, View } from "react-native";
import type { PendingApproval, Snapshot } from "../../../../pkg/remote/web/src/protocol3";
import type { NativeClient } from "../core/client";
import { Button, Notice } from "../ui/components";
import { offlineApprovalInput } from "../ui/offline-approvals";
import { styles, tokens } from "../ui/theme";

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
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [answered, setAnswered] = useState(false);
  const locked = useRef(false);
  const { approval } = pending;
  async function queue(decision: "allow" | "deny"): Promise<void> {
    if (locked.current || answered) return;
    locked.current = true;
    setBusy(true);
    setError(null);
    try {
      const result = await client.queue(cid, offlineApprovalInput(pending, decision, reason));
      setAnswered(true);
      setNotice(
        result.state === "sent"
          ? "Answer delivered to the relay. It will only run if this exact approval is still pending."
          : "Answer saved on this phone. It has not reached the relay yet.",
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
    <View style={[styles.card, { borderColor: "#765C36" }]}>
      <View style={styles.between}>
        <Text style={[styles.eyebrow, { color: tokens.warning }]}>Saved approval</Text>
        <Text style={styles.secondary}>{approval.agent}</Text>
      </View>
      <Text style={styles.heading}>{approval.description || approval.kind}</Text>
      {approval.path && (
        <Text selectable style={styles.code}>
          {approval.path}
        </Text>
      )}
      {(approval.command || approval.diff || approval.raw) && (
        <ScrollView nestedScrollEnabled style={{ maxHeight: 200 }}>
          <Text selectable style={styles.code}>
            {(approval.command || approval.diff || approval.raw || "").slice(0, 24_000)}
          </Text>
        </ScrollView>
      )}
      {(approval.command_truncated ||
        approval.diff_truncated ||
        approval.raw_truncated ||
        (approval.command || approval.diff || approval.raw || "").length > 24_000) && (
        <Text style={styles.secondary}>
          Preview truncated. Review the full action on your computer.
        </Text>
      )}
      {approval.tier !== "normal" && (
        <Notice>
          Destructive approval requires live passkey verification. You can still queue a denial.
        </Notice>
      )}
      <TextInput
        accessibilityLabel="Reason for queued denial, optional"
        placeholder="Reason for denying (optional)"
        placeholderTextColor={tokens.muted}
        style={styles.input}
        value={reason}
        onChangeText={setReason}
        maxLength={2_000}
        multiline
        editable={!busy && !answered}
      />
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
        <Button
          title="Approve when online"
          tone="primary"
          busy={busy}
          disabled={answered || approval.tier !== "normal"}
          onPress={() => void queue("allow")}
        />
        <Button
          title="Queue denial"
          tone="danger"
          disabled={busy || answered}
          onPress={() => void queue("deny")}
        />
      </View>
      {notice && <Notice>{notice}</Notice>}
      {error && <Notice error>{error}</Notice>}
    </View>
  );
}

export function OfflineApprovals({ client, cid, snapshot, onQueued }: OfflineApprovalsProps) {
  if (!snapshot.approvals.length) return null;
  return (
    <View style={{ gap: 12 }}>
      <Text accessibilityRole="header" style={styles.heading}>
        Saved approvals
      </Text>
      <Notice>
        These requests may already have changed. Answers expire after 1 hour and are bound to the
        saved approval ID and digest.
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
