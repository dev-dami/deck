import type * as preact from "preact";
import type { ComputerView } from "../state3";

export interface ComputersScreenProps {
  computers: ComputerView[];
  onSelect: (cid: string) => void;
  onRetry: (cid: string) => void;
}

function statusText(status: ComputerView["status"]): string {
  switch (status) {
    case "online":
      return "online";
    case "connecting":
      return "connecting…";
    case "offline":
      return "offline";
    case "needs-pairing":
      return "needs pairing";
    case "revoked":
      return "revoked";
  }
}

export function ComputersScreen(props: ComputersScreenProps): preact.JSX.Element {
  const { computers, onSelect, onRetry } = props;
  if (computers.length === 0) {
    return (
      <main>
        <h1>Deck</h1>
        <p>No computers paired yet. Run `deck mobile pair` on your PC and scan the QR.</p>
      </main>
    );
  }
  return (
    <main>
      <h1>Deck</h1>
      <ul>
        {computers.map((c) => {
          const needs = c.inbox.filter((i) => i.state === "needs_you").length;
          return (
            <li key={c.record.cid}>
              <button type="button" onClick={() => onSelect(c.record.cid)}>
                {c.record.name} · {statusText(c.status)}
                {needs > 0 ? ` · ${needs} need you` : ""}
              </button>
              {c.error !== null ? <p role="alert">{c.error}</p> : null}
              {c.status === "offline" || c.status === "needs-pairing" ? (
                <button type="button" onClick={() => onRetry(c.record.cid)}>
                  Retry
                </button>
              ) : null}
            </li>
          );
        })}
      </ul>
    </main>
  );
}
