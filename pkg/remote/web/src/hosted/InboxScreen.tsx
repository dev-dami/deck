import type * as preact from "preact";
import type { GroupedInbox } from "../state3";
import { type ComputerView, offlineAge } from "../state3";

export interface InboxScreenProps {
  computer: ComputerView;
  nowMs: number;
  onBack: () => void;
  onAck: (pj: string, sessionId: string) => void;
  onRetry: () => void;
  onSelect?: (pj: string, sessionId: string) => void;
}

export function InboxScreen(props: InboxScreenProps): preact.JSX.Element {
  const { computer, nowMs, onBack, onAck, onRetry } = props;
  const age = offlineAge(computer.lastSeen, nowMs);
  const grouped: GroupedInbox = { needsYou: [], running: [], done: [] };
  for (const item of computer.inbox) {
    if (item.state === "needs_you") grouped.needsYou.push(item);
    else if (item.state === "running") grouped.running.push(item);
    else grouped.done.push(item);
  }
  return (
    <main>
      <button type="button" onClick={onBack}>
        ← Computers
      </button>
      <h1>{computer.record.name}</h1>
      {computer.status !== "online" ? (
        <p>
          {age ?? "offline"}
          <button type="button" onClick={onRetry}>
            Retry
          </button>
        </p>
      ) : null}
      {computer.error !== null ? <p role="alert">{computer.error}</p> : null}
      <section aria-label="Needs you">
        <h2>Needs you</h2>
        {grouped.needsYou.length === 0 ? (
          <p>Nothing waiting.</p>
        ) : (
          <ul>
            {grouped.needsYou.map((item) => (
              <li key={`${item.pj}:${item.session_id}`}>
                <button type="button" onClick={() => props.onSelect?.(item.pj, item.session_id)}>
                  {item.title}
                </button>{" "}
                · {item.summary}
                {item.approvals > 0 ? ` · ${item.approvals} approval(s)` : ""}
                <button type="button" onClick={() => onAck(item.pj, item.session_id)}>
                  Mark seen
                </button>
              </li>
            ))}
          </ul>
        )}
      </section>
      <section aria-label="Running">
        <h2>Running</h2>
        {grouped.running.length === 0 ? (
          <p>Nothing running.</p>
        ) : (
          <ul>
            {grouped.running.map((item) => (
              <li key={`${item.pj}:${item.session_id}`}>
                <button type="button" onClick={() => props.onSelect?.(item.pj, item.session_id)}>
                  {item.title}
                </button>{" "}
                · {item.summary}
              </li>
            ))}
          </ul>
        )}
      </section>
      <section aria-label="Done">
        <h2>Done</h2>
        {grouped.done.length === 0 ? (
          <p>Nothing done yet.</p>
        ) : (
          <ul>
            {grouped.done.map((item) => (
              <li key={`${item.pj}:${item.session_id}`}>
                <button type="button" onClick={() => props.onSelect?.(item.pj, item.session_id)}>
                  {item.title}
                </button>{" "}
                · {item.summary}
              </li>
            ))}
          </ul>
        )}
      </section>
    </main>
  );
}
