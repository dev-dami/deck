import type { QueuedItem } from "../protocol";
import { useController } from "./hooks";
import { Sheet } from "./Sheet";

interface QueueSheetProps {
  sessionId: string;
  items: QueuedItem[];
  onClose: () => void;
}

export function QueueSheet({ sessionId, items, onClose }: QueueSheetProps) {
  const controller = useController();
  return (
    <Sheet title="Queued messages" onClose={onClose}>
      {items.length === 0 ? (
        <p class="muted">Nothing is waiting.</p>
      ) : (
        <ul class="list">
          {items.map((q) => (
            <li key={q.id} class="queue-item">
              <p class="queue-agent muted small-text">${q.agent}</p>
              <p class="queue-text">{q.text}</p>
              {q.text_truncated === true ? (
                <p class="muted small-text">Text truncated by Deck.</p>
              ) : null}
              <div class="btn-row">
                <button
                  type="button"
                  class="btn"
                  onClick={() => void controller.steer(sessionId, q.id)}
                >
                  Steer now
                </button>
                <button
                  type="button"
                  class="btn ghost"
                  onClick={() => void controller.cancelQueued(sessionId, q.id)}
                >
                  Cancel
                </button>
              </div>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}
