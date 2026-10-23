import { useRef, useState } from "react";
import { ScrollView, Text, TextInput, View } from "react-native";
import type {
  AnswerDecision,
  ApprovalV3,
  Assertion,
} from "../../../../pkg/remote/web/src/protocol3";
import { Button, Notice } from "../ui/components";
import { DiffView } from "../ui/DiffView";
import { useTheme } from "../ui/ThemeContext";

export interface ApprovalPanelProps {
  approval: ApprovalV3;
  online: boolean;
  onAnswer(decision: AnswerDecision, reason?: string, assertion?: Assertion): Promise<void>;
  authenticate?: () => Promise<Assertion>;
  // Position among the session's open approvals, as the desktop card shows it.
  index?: number;
  count?: number;
}

const PREVIEW_LIMIT = 24_000;

function kindLabel(approval: ApprovalV3): string {
  return approval.kind === "command"
    ? "Command approval"
    : approval.kind === "edit"
      ? "File change approval"
      : "Approval";
}

// The one line that says what the agent wants to do (the desktop's approvalSummary).
function summary(approval: ApprovalV3): string {
  if (approval.kind === "command" && approval.command) return approval.command;
  if (approval.kind === "edit" && approval.path) return approval.path;
  return approval.description;
}

export function ApprovalPanel({
  approval,
  onAnswer,
  authenticate,
  online,
  index,
  count,
}: ApprovalPanelProps) {
  const { styles, tokens } = useTheme();
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
  const what = summary(approval);
  const preview = approval.command || approval.raw || "";
  const showCommand = approval.kind !== "edit" && preview !== "" && preview !== what;
  const truncated =
    approval.command_truncated ||
    approval.diff_truncated ||
    approval.raw_truncated ||
    preview.length > PREVIEW_LIMIT;
  return (
    <View
      accessibilityRole="summary"
      accessibilityLabel={kindLabel(approval)}
      style={[
        styles.card,
        { borderColor: destructive ? tokens.warning : tokens.border, borderRadius: 18 },
      ]}
    >
      <View style={styles.between}>
        <Text style={{ color: tokens.warning, fontSize: 12, fontWeight: "600" }}>
          {destructive ? "Passkey required" : kindLabel(approval)}
        </Text>
        <Text style={[styles.secondary, { fontSize: 12 }]} numberOfLines={1}>
          {approval.agent}
          {count !== undefined && count > 1 ? `  ·  ${(index ?? 0) + 1}/${count}` : ""}
        </Text>
      </View>
      {approval.description !== "" && approval.description !== what && (
        <Text style={styles.secondary}>{approval.description}</Text>
      )}
      <ScrollView
        style={{
          maxHeight: 160,
          borderRadius: 10,
          borderWidth: 1,
          borderColor: tokens.border,
          backgroundColor: tokens.code,
        }}
        nestedScrollEnabled
        contentContainerStyle={{ padding: 10 }}
      >
        <Text selectable style={styles.code}>
          {what.slice(0, PREVIEW_LIMIT)}
        </Text>
      </ScrollView>
      {showCommand && (
        <ScrollView style={{ maxHeight: 200 }} nestedScrollEnabled>
          <Text selectable style={[styles.code, { color: tokens.secondary }]}>
            {preview.slice(0, PREVIEW_LIMIT)}
          </Text>
        </ScrollView>
      )}
      {approval.diff ? (
        <ScrollView style={{ maxHeight: 320 }} nestedScrollEnabled>
          <DiffView
            diff={approval.diff.slice(0, PREVIEW_LIMIT)}
            truncated={approval.diff_truncated}
          />
        </ScrollView>
      ) : null}
      {truncated && (
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
        accessibilityLabel="Reason for declining, optional"
        placeholder="Reason for declining (optional)"
        placeholderTextColor={tokens.muted}
        value={reason}
        onChangeText={setReason}
        maxLength={2_000}
        multiline
        style={[styles.input, { minHeight: 44, paddingVertical: 10 }]}
        editable={!busy}
      />
      <View style={{ flexDirection: "row", flexWrap: "wrap", gap: 8, justifyContent: "flex-end" }}>
        <Button
          title="Decline"
          tone="ghost"
          disabled={busy || !online}
          onPress={() => void answer("deny")}
        />
        {!destructive && (
          <Button
            title="Always allow this session"
            disabled={busy || !online}
            onPress={() => void answer("allow_session")}
          />
        )}
        <Button
          title={destructive ? "Verify & approve" : "Approve"}
          tone="primary"
          busy={busy}
          disabled={!online || (destructive && !authenticate)}
          onPress={() => void answer("allow")}
        />
      </View>
      {error && <Notice error>{error}</Notice>}
    </View>
  );
}
