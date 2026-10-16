import { queueAction } from "../offline/queue";
import { decodePushMessage, type PushMessage } from "../protocol3";
import { listComputers } from "../store/computers";

export function parsePush(value: unknown): PushMessage | null {
  return decodePushMessage(value);
}

export function notificationActions(message: PushMessage): { action: string; title: string }[] {
  return message.cid &&
    message.kind === "approval" &&
    message.tier === "normal" &&
    message.approval_id &&
    message.digest
    ? [
        { action: "approve", title: "Approve" },
        { action: "deny", title: "Deny" },
      ]
    : [];
}

export function notificationRoute(message: PushMessage): string {
  return message.cid
    ? `#/c/${encodeURIComponent(message.cid)}/p/${encodeURIComponent(message.pj)}/s/${encodeURIComponent(message.session_id)}`
    : "#/computers";
}

// A missing/revoked computer or destructive request must always be reviewed
// in the app. A push body alone never grants authority to another computer.
export async function answerNotification(
  message: PushMessage,
  action: string,
  deps: { list?: typeof listComputers; queue?: typeof queueAction } = {},
): Promise<boolean> {
  if (!notificationActions(message).some((item) => item.action === action)) return false;
  const computer = (await (deps.list ?? listComputers)()).find((c) => c.cid === message.cid);
  if (!computer?.phoneKey || !computer.deviceId || !computer.relayToken) return false;
  await (deps.queue ?? queueAction)(computer, {
    kind: "approvals.answer",
    pj: message.pj,
    sessionId: message.session_id,
    payload: {
      session_id: message.session_id,
      id: message.approval_id,
      decision: action === "approve" ? "allow" : "deny",
    },
    precond: { approval_id: message.approval_id as string, digest: message.digest as string },
    expiresInMs: 3_600_000,
  });
  return true;
}
