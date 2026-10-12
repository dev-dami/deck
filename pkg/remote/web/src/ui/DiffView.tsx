import { useMemo, useState } from "preact/hooks";
import { parseDiff } from "../diff";

const INITIAL_LINES = 400;

export function DiffView({ diff, truncated }: { diff: string; truncated?: boolean }) {
  const lines = useMemo(() => parseDiff(diff), [diff]);
  const [all, setAll] = useState(false);
  const shown = all ? lines : lines.slice(0, INITIAL_LINES);
  return (
    <div class="diff">
      <pre class="diff-body">
        {shown.map((l, i) => (
          <span key={i} class={`dl dl-${l.kind}`}>
            {l.text === "" ? " " : l.text}
            {"\n"}
          </span>
        ))}
      </pre>
      {!all && lines.length > INITIAL_LINES ? (
        <button type="button" class="btn small" onClick={() => setAll(true)}>
          Show {lines.length - INITIAL_LINES} more lines
        </button>
      ) : null}
      {truncated === true ? <p class="muted small-text">Diff truncated by Deck.</p> : null}
    </div>
  );
}
