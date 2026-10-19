import { useRef, useState } from "react";
import { ScrollView, Text, TextInput, View } from "react-native";
import type {
  AnswerDecision,
  ApprovalV3,
  Assertion,
} from "../../../../pkg/remote/web/src/protocol3";
import { Button, Notice } from "../ui/components";
import { styles, tokens } from "../ui/theme";

export interface ApprovalPanelProps {
  approval: ApprovalV3;
  online: boolean;
  onAnswer(decision: AnswerDecision, reason?: string, assertion?: Assertion): Promise<void>;
  authenticate?: () => Promise<Assertion>;
}

export function ApprovalPanel({ approval, onAnswer, authenticate, online }: ApprovalPanelProps) {
  const [reason, setReason] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const pending = useRef(false);
  const destructive = approval.tier !== "normal";
  async function answer(decision: AnswerDecision): Promise<void> {
    if (pending.current) return;
    pending.current = true;
    setBusy(true);
    setError(null);
    try {
      let assertion: Assertion | undefined;
      if (decision !== "deny" && destructive) {
        if (!authenticate)
          throw new Error(
            "Set up a passkey for this app and relay before approving destructive actions.",
          );
        assertion = await authenticate();
      }
      await onAnswer(
        decision,
        decision === "deny" && reason.trim() ? reason.trim() : undefined,
        assertion,
      );
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : "The approval could not be sent.");
    } finally {
      pending.current = false;
      setBusy(false);
    }
  }
  return (
    <View
      style={[
        styles.card,
        { borderColor: destructive ? "#765C36" : tokens.border, marginBottom: 12 },
      ]}
    >
      <View style={styles.between}>
        <Text style={[styles.eyebrow, { color: tokens.warning }]}>
          {destructive ? "Passkey required" : "Needs your approval"}
        </Text>
        <Text style={styles.secondary}>{approval.agent}</Text>
      </View>
      <Text style={styles.heading}>{approval.description || approval.kind}</Text>
      {approval.path && (
        <Text selectable style={styles.code}>
          {approval.path}
        </Text>
      )}
      {(approval.command || approval.diff || approval.raw) && (
        <ScrollView style={{ maxHeight: 240 }} nestedScrollEnabled>
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
          Preview is truncated. Review the full action on your computer before approving.
        </Text>
      )}
      {destructive && (
        <Text style={styles.secondary}>
          This action needs a cryptographic passkey assertion. Your device will ask you to verify.
        </Text>
      )}
      {!online && <Notice>Reconnect to answer this approval.</Notice>}
      <TextInput
        accessibilityLabel="Reason for denying approval, optional"
        placeholder="Reason for denying (optional)"
        placeholderTextColor={tokens.muted}
        value={reason}
        onChangeText={setReason}
        maxLength={2_000}
        multiline
        style={styles.input}
        editable={!busy}
      />
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8 }}>
        <Button
          title={destructive ? "Verify & approve" : "Approve once"}
          tone="primary"
          busy={busy}
          disabled={!online || (destructive && !authenticate)}
          onPress={() => void answer("allow")}
        />
        {!destructive && (
          <Button
            title="Allow session"
            disabled={busy || !online}
            onPress={() => void answer("allow_session")}
          />
        )}
        <Button
          title="Deny"
          tone="danger"
          disabled={busy || !online}
          onPress={() => void answer("deny")}
        />
      </View>
      {error && <Notice error>{error}</Notice>}
    </View>
  );
}
