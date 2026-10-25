import { parseDiff } from "@web/diff";
import { MessageSquarePlus, Plus } from "lucide-react";
import { createContext, useContext, useMemo, useState } from "react";
import { diffRows } from "../diffRows";
import { Button } from "./kit/button";

// What the open room does with a comment on a line of its diff: it goes to
// the composer, addressed to the agent that changed the file. count is how
// many the draft holds; send sends the draft.
export interface LineCommentApi {
  count: number;
  add(path: string, line: number, text: string, comment: string): void;
  send(): void;
}

export const LineComments = createContext<LineCommentApi | null>(null);

// "2 comments · Send", for the diff's header while the draft holds comments.
export function CommentsBadge() {
  const api = useContext(LineComments);
  if (api === null || api.count === 0) return null;
  return (
    <span className="line-comments inline-flex items-center gap-1.5 font-mono text-2xs text-muted-foreground tabular-nums">
      <MessageSquarePlus className="size-3.5 text-faint" />
      {api.count} comment{api.count === 1 ? "" : "s"}
      <span aria-hidden className="text-faint">
        ·
      </span>
      <Button variant="link" size="xs" className="h-auto px-0 font-sans" onClick={api.send}>
        Send
      </Button>
    </span>
  );
}

function CommentInput({ where, onSubmit, onCancel }: { where: string; onSubmit: (text: string) => void; onCancel: () => void }) {
  const [text, setText] = useState("");
  return (
    <form
      className="diff-comment flex items-center gap-2 border-hairline border-y bg-surface-1 px-3 py-1.5 font-sans"
      onSubmit={(e) => {
        e.preventDefault();
        if (text.trim() !== "") onSubmit(text);
      }}
    >
      <input
        autoFocus
        aria-label={`Comment on ${where}`}
        placeholder={`Comment on ${where}`}
        value={text}
        className="min-w-0 flex-1 bg-transparent text-[13px] outline-none placeholder:text-faint"
        onChange={(e) => setText(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Escape") onCancel();
        }}
      />
      <Button type="submit" size="xs" variant="outline" disabled={text.trim() === ""}>
        Comment
      </Button>
    </form>
  );
}

// numbered: the diff panel's view, with line numbers, change bars and
// folds between hunks. Without it, the compact view of a tool call. With a
// path in an open room, each numbered line takes a comment.
export function DiffView({
  diff,
  truncated,
  numbered = false,
  path,
}: {
  diff: string;
  truncated?: boolean | undefined;
  numbered?: boolean;
  path?: string;
}) {
  const lines = useMemo(() => (numbered ? [] : parseDiff(diff)), [diff, numbered]);
  const rows = useMemo(() => (numbered ? diffRows(diff) : []), [diff, numbered]);
  const api = useContext(LineComments);
  const [at, setAt] = useState<number | null>(null);
  const comments = api !== null && path !== undefined;
  if (numbered) {
    return (
      <div className="diff diff-numbered py-1" role="table" aria-label="Diff">
        {rows.map((r, i) =>
          r.k === "fold" ? (
            <div key={i} className="diff-fold" role="row">
              {r.skipped === null ? "…" : `${r.skipped} unmodified line${r.skipped === 1 ? "" : "s"}`}
            </div>
          ) : r.k === "meta" ? (
            <div key={i} className="diff-line diff-meta" role="row">
              {r.text}
            </div>
          ) : (
            <div key={i} role="rowgroup">
              <div className={`diff-row diff-${r.kind}`} role="row" data-line={r.num ?? undefined}>
                <span className="diff-gutter">
                  {comments && r.num !== null && (
                    <button
                      type="button"
                      className="diff-add-comment"
                      aria-label={`Comment on line ${r.num}`}
                      title="Comment on this line"
                      onClick={() => setAt(at === i ? null : i)}
                    >
                      <Plus className="size-3" />
                    </button>
                  )}
                  {r.num}
                </span>
                <span className="diff-code">{r.text === "" ? " " : r.text}</span>
              </div>
              {comments && at === i && r.num !== null && (
                <CommentInput
                  where={`${path}:${r.num}`}
                  onCancel={() => setAt(null)}
                  onSubmit={(text) => {
                    api.add(path, r.num ?? 0, r.text, text);
                    setAt(null);
                  }}
                />
              )}
            </div>
          ),
        )}
        {truncated === true && <div className="diff-line diff-meta">The diff is cut short.</div>}
      </div>
    );
  }
  return (
    <div className="diff py-1" role="table" aria-label="Diff">
      {lines.map((l, i) => (
        <div key={i} className={`diff-line diff-${l.kind}`} role="row">
          {l.text === "" ? " " : l.text}
        </div>
      ))}
      {truncated === true && <div className="diff-line diff-meta">The diff is cut short.</div>}
    </div>
  );
}
