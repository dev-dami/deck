import type { JSX } from "preact";
import { useEffect, useState } from "preact/hooks";
import { flushOutbox, queueAction } from "../offline/queue";
import { type LoadedSnapshot, loadSnapshot } from "../offline/snapshot";
import { decodeReceipt, type Receipt } from "../protocol3";
import type { ComputerView, SessionLike } from "../state3";
import { type OutboxEntry, outboxList, outboxRemove } from "../store/outbox";
import { listSubmitted, type SubmittedAction } from "../store/submitted";

export function OfflinePanel(props: {
  computer: ComputerView;
  session: SessionLike | null;
  onSnapshot: (snapshot: LoadedSnapshot) => void;
}): JSX.Element {
  const [entries, setEntries] = useState<OutboxEntry[]>([]);
  const [submitted, setSubmitted] = useState<SubmittedAction[]>([]);
  const [receipts, setReceipts] = useState<Receipt[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [items, setItems] = useState(props.computer.inbox);
  const [notice, setNotice] = useState<string | null>(null);
  const cid = props.computer.record.cid;
  const refresh = async (): Promise<void> => {
    setEntries(await outboxList(cid));
    setSubmitted(await listSubmitted(cid));
    if (props.session !== null) {
      const result = await props.session.call<unknown>("queue.status");
      if (Array.isArray(result))
        setReceipts(result.map(decodeReceipt).filter((r): r is Receipt => r !== null));
    }
  };
  useEffect(() => {
    let active = true;
    void refresh().catch((err: unknown) => {
      if (active) setError(String(err));
    });
    if (props.computer.status === "offline" || props.computer.status === "connecting") {
      void loadSnapshot(props.computer.record)
        .then((snapshot) => {
          if (active && snapshot !== null) {
            props.onSnapshot(snapshot);
            setReceipts(snapshot.snapshot.receipts);
            setItems(snapshot.snapshot.inbox);
          }
        })
        .catch((err: unknown) => {
          if (active) setError(String(err));
        });
    }
    props.session?.onEvent((event) => {
      if (active && event.name === "receipts.changed") void refresh().catch(() => undefined);
    });
    return () => {
      active = false;
    };
  }, [cid, props.session, props.computer.status]);
  const run = (work: () => Promise<void>): void => {
    if (busy) return;
    setBusy(true);
    setError(null);
    void work()
      .then(refresh)
      .catch((err: unknown) =>
        setError(err instanceof Error ? err.message : "Queue action failed."),
      )
      .finally(() => setBusy(false));
  };
  return (
    <section aria-label="Queue and receipts">
      <h2>Queue and receipts</h2>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          const form = event.currentTarget;
          const data = new FormData(form);
          const item = [...props.computer.inbox, ...items].find(
            (item) => `${item.pj}:${item.session_id}` === data.get("session"),
          );
          const text = String(data.get("text") ?? "").trim();
          if (!item || !text) return;
          run(async () => {
            const result = await queueAction(props.computer.record, {
              kind: "chat.send",
              pj: item.pj,
              sessionId: item.session_id,
              payload: { text },
              expiresInMs: Number(data.get("expiry")),
            });
            setNotice(
              `${result.qid}: ${result.state === "sent" ? "Delivered to relay; waiting for computer" : "Saved on phone; waiting for network"}`,
            );
            form.reset();
          });
        }}
      >
        <label>
          Session
          <select name="session" required>
            {(props.computer.inbox.length ? props.computer.inbox : items).map((item) => (
              <option key={`${item.pj}:${item.session_id}`} value={`${item.pj}:${item.session_id}`}>
                {item.title}
              </option>
            ))}
          </select>
        </label>
        <label>
          Prompt for when the computer is available
          <textarea name="text" rows={3} required />
        </label>
        <label>
          Expires after
          <select name="expiry" defaultValue="28800000">
            <option value="3600000">1 hour</option>
            <option value="28800000">8 hours</option>
            <option value="86400000">24 hours</option>
          </select>
        </label>
        <button type="submit" disabled={busy || (!props.computer.inbox.length && !items.length)}>
          Queue prompt
        </button>
      </form>
      {notice && <p role="status">{notice}</p>}
      <p>
        Saved on this phone: {entries.length}. Delivered actions appear in receipts after the
        computer processes them.
      </p>
      <button
        type="button"
        disabled={busy}
        onClick={() =>
          run(async () => {
            await flushOutbox(props.computer.record);
          })
        }
      >
        Send waiting actions
      </button>
      {entries.map((entry) => (
        <p key={entry.qid}>
          {entry.qid} · expires {new Date(entry.expires_at).toLocaleString()}{" "}
          <button type="button" disabled={busy} onClick={() => run(() => outboxRemove(entry.qid))}>
            Remove unsent action
          </button>
        </p>
      ))}
      {receipts.map((receipt) => (
        <p key={receipt.qid}>
          {receipt.qid}: {receipt.status}
          {receipt.reason ? ` · ${receipt.reason}` : ""}
        </p>
      ))}
      {submitted
        .filter((entry) => !receipts.some((receipt) => receipt.qid === entry.qid))
        .map((entry) => (
          <p key={entry.qid}>
            {entry.kind}: {entry.qid} ·{" "}
            {Date.parse(entry.expires_at) <= Date.now()
              ? "Expired; awaiting receipt"
              : "Delivered to relay; waiting for computer"}
            {entry.kind !== "queue.cancel" && (
              <button
                type="button"
                disabled={busy}
                onClick={() =>
                  run(async () => {
                    await queueAction(props.computer.record, {
                      kind: "queue.cancel",
                      payload: { qid: entry.qid },
                    });
                    setNotice(
                      "Cancellation requested. Execution may already have started; check receipts.",
                    );
                  })
                }
              >
                Request cancellation
              </button>
            )}
          </p>
        ))}
      <form
        onSubmit={(event) => {
          event.preventDefault();
          const data = new FormData(event.currentTarget);
          const qid = String(data.get("qid") ?? "").trim();
          if (qid)
            run(async () => {
              await queueAction(props.computer.record, { kind: "queue.cancel", payload: { qid } });
            });
        }}
      >
        <label>
          Cancel a delivered action by id
          <input name="qid" required />
        </label>
        <button type="submit" disabled={busy}>
          Request cancellation
        </button>
      </form>
      {error && <p role="alert">{error}</p>}
    </section>
  );
}
