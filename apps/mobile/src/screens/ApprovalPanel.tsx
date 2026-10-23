import { useRef, useState } from "react";
import { ScrollView, Text, View } from "react-native";
import type {
  AnswerDecision,
  ApprovalV3,
  Assertion,
} from "../../../../pkg/remote/web/src/protocol3";
import { Button, Dot, Notice } from "../ui/components";
import { useTheme } from "../ui/theme";
import { rgba } from "../ui/themes";

export interface ApprovalPanelProps {
  approval: ApprovalV3;
  online: boolean;
  more?: number;
  onAnswer(decision: AnswerDecision, reason?: string, assertion?: Assertion): Promise<void>;
  authenticate?: () => Promise<Assertion>;
}

// Docked above the composer: one pending approval at a time, the action
// readable at a glance and the three answers in thumb reach.
export function ApprovalPanel({
  approval,
  onAnswer,
  authenticate,
  online,
  more = 0,
}: ApprovalPanelProps) {
  const { t, s } = useTheme();
  const [busy, setBusy] = useState<AnswerDecision | null>(null);
  const [error, setError] = useState<string | null>(null);
  const pending = useRef(false);
  const destructive = approval.tier !== "normal";
  const preview = approval.command || approval.diff || approval.raw || "";
  const truncated =
    approval.command_truncated ||
    approval.diff_truncated ||
    approval.raw_truncated ||
    preview.length > 24_000;

  async function answer(decision: AnswerDecision): Promise<void> {
    if (pending.current) return;
    pending.current = true;
    setBusy(decision);
    setError(null);
    try {
      let assertion: Assertion | undefined;
      if (decision !== "deny" && destructive) {
        if (!authenticate)
          throw new Error("Add a passkey in Settings before approving destructive actions.");
        assertion = await authenticate();
      }
      await onAnswer(decision, undefined, assertion);
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "The answer could not be sent.");
    } finally {
      pending.current = false;
      setBusy(null);
    }
  }

  return (
    <View
      accessibilityLabel="Approval needed"
      style={[
        s.float,
        {
          borderRadius: 24,
          padding: 16,
          gap: 12,
          boxShadow: `${t.shadow.float}, 0 0 0 1.5px ${rgba(destructive ? t.danger : t.warn, 0.55)}`,
        },
      ]}
    >
      <View style={s.row}>
        <Dot color={destructive ? t.danger : t.warn} />
        <Text style={[s.heading, { flex: 1 }]} numberOfLines={2}>
          {approval.description || approval.kind}
        </Text>
        <Text style={s.small}>
          {approval.agent}
          {more > 0 ? ` · +${more} more` : ""}
        </Text>
      </View>
      {approval.path && (
        <Text selectable style={s.code}>
          {approval.path}
        </Text>
      )}
      {preview !== "" && (
        <ScrollView style={{ maxHeight: 160 }} nestedScrollEnabled>
          <Text selectable style={s.code}>
            {preview.slice(0, 24_000)}
          </Text>
        </ScrollView>
      )}
      {truncated && (
        <Text style={s.small}>Preview cut short. Check the full action on your computer.</Text>
      )}
      {destructive && <Text style={s.small}>Destructive. Your passkey confirms it's you.</Text>}
      {!online && <Notice>Reconnect to answer.</Notice>}
      <View style={{ flexDirection: "row", gap: 8 }}>
        <Button
          title="Deny"
          style={{ flex: 1 }}
          busy={busy === "deny"}
          disabled={busy !== null || !online}
          onPress={() => void answer("deny")}
        />
        {!destructive && (
          <Button
            title="Always"
            label="Allow for this session"
            style={{ flex: 1 }}
            busy={busy === "allow_session"}
            disabled={busy !== null || !online}
            onPress={() => void answer("allow_session")}
          />
        )}
        <Button
          title="Approve"
          icon={destructive ? "key" : undefined}
          tone="primary"
          style={{ flex: 1.2 }}
          busy={busy === "allow"}
          disabled={busy !== null || !online || (destructive && !authenticate)}
          onPress={() => void answer("allow")}
        />
      </View>
      {error && <Text style={s.error}>{error}</Text>}
    </View>
  );
}
