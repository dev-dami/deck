import { parseDiff } from "@web/diff";
import { Columns2, MessageSquarePlus, Plus, Rows2, WrapText } from "./icons";
import { createContext, Fragment, type ReactNode, useContext, useMemo, useState } from "react";
import {
  type DiffRow,
  diffRows,
  foldContext,
  type HunkRow,
  isFold,
  type LineRow,
  pairRows,
  pathOfDiff,
  showHunk,
  type SplitRow,
  splitRows,
} from "../diffRows";
import { highlightRows, langOf, markSpans, plain, type Seg } from "../highlight";
import { cn } from "../lib/cn";
import { setPrefs, usePrefs } from "../prefs";
import { type Span, wordDiff } from "../wordDiff";
import { Button } from "./kit/button";
import { SegLine } from "./Syntax";

// What the open room does with a comment on a line of its diff: it goes to
// the composer, addressed to the agent that changed the file. count is how
// many the draft holds; send sends the draft.
export interface LineCommentApi {
  count: number;
  add(path: string, line: number, text: string, comment: string): void;
  send(): void;
}

export const LineComments = createContext<LineCommentApi | null>(null);

// Whether the diffs below may show split: the panel is wide enough.
export const SplitAllowed = createContext(false);

// "2 comments · Send", for the diff's header while the draft holds comments.
export function CommentsBadge() {
  const api = useContext(LineComments);
  if (api === null || api.count === 0) return null;
  return (
    <span className="line-comments inline-flex h-6 animate-enter items-center gap-1.5 rounded-full bg-hover pr-1 pl-2.5 text-2xs text-muted-foreground tabular-nums">
      <MessageSquarePlus className="size-3.5 text-faint" />
      {api.count} comment{api.count === 1 ? "" : "s"}
      <Button variant="ghost" size="xs" className="h-5 px-2 font-medium" onClick={api.send}>
        Send
      </Button>
    </span>
  );
}

// The Changes header's view toggles: unified or split (when the panel is
// wide enough), and wrap.
export function DiffToggles({ splitOk }: { splitOk: boolean }) {
  const p = usePrefs();
  const split = splitOk && p.diffView === "split";
  return (
    <>
      {splitOk && (
        <Button
          variant="ghost-muted"
          size="icon-sm"
          className="diff-layout"
          aria-label={split ? "Unified view" : "Split view"}
          title={split ? "Unified view" : "Split view"}
          aria-pressed={split}
          onClick={() => setPrefs({ diffView: split ? "unified" : "split" })}
        >
          {split ? <Rows2 /> : <Columns2 />}
        </Button>
      )}
      <Button
        variant="ghost-muted"
        size="icon-sm"
        className="diff-wrap-toggle"
        aria-label="Wrap long lines"
        title="Wrap long lines"
        aria-pressed={p.diffWrap}
        onClick={() => setPrefs({ diffWrap: !p.diffWrap })}
      >
        <WrapText />
      </Button>
    </>
  );
}

function CommentInput({ where, onSubmit, onCancel }: { where: string; onSubmit: (text: string) => void; onCancel: () => void }) {
  const [text, setText] = useState("");
  return (
    <form
      className="diff-comment flex items-center gap-2 border-hairline border-y bg-surface-1 px-2.5 py-2 font-sans"
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
        className="h-7 min-w-0 flex-1 rounded-full bg-hover px-3 text-[13px] outline-none ring-1 ring-hairline transition-shadow placeholder:text-faint focus:ring-2 focus:ring-ring"
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

const unmodified = (n: number) => `${n} unmodified line${n === 1 ? "" : "s"}`;

function HunkHead({ r }: { r: HunkRow }) {
  return (
    <div className="diff-hunk-row" role="row">
      <span className="diff-hunk-head">{r.header}</span>
      {r.context !== "" && <span className="diff-hunk-ctx">{r.context}</span>}
      {r.skipped !== null && <span className="diff-hunk-skip">{unmodified(r.skipped)}</span>}
    </div>
  );
}

function FoldRow({ n, onOpen }: { n: number; onOpen: () => void }) {
  return (
    <div className="diff-fold-row" role="row">
      <button type="button" className="diff-fold" onClick={onOpen} title="Show the lines">
        {unmodified(n)}
      </button>
    </div>
  );
}

// Everything a numbered diff draws from, worked out once per diff.
function useModel(diff: string, path: string | undefined) {
  return useMemo(() => {
    const rows = diffRows(diff);
    const lang = langOf(path ?? pathOfDiff(diff) ?? "");
    const hl = highlightRows(rows, lang);
    const pairs = pairRows(rows);
    // The changed words of each paired line.
    const words = new Map<number, Span[]>();
    for (const [d, a] of pairs) {
      const dr = rows[d];
      const ar = rows[a];
      if (dr?.k !== "line" || ar?.k !== "line" || dr.kind !== "del") continue;
      const w = wordDiff(dr.text, ar.text);
      if (w === null) continue;
      words.set(d, w.del);
      words.set(a, w.add);
    }
    const segs = (i: number): Seg[] => {
      const r = rows[i] as LineRow;
      const base = hl[i] ?? plain(r.text);
      const w = words.get(i);
      return w === undefined ? base : markSpans(base, w);
    };
    return { rows, segs, split: splitRows(rows, pairs) };
  }, [diff, path]);
}

// numbered: the diff panel's view, with line numbers, change bars, hunk
// headers and syntax colour. Without it, the compact view of a tool call.
// With a path in an open room, each numbered line takes a comment.
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
  if (!numbered) return <CompactDiff diff={diff} truncated={truncated} />;
  return <NumberedDiff diff={diff} truncated={truncated} path={path} />;
}

function CompactDiff({ diff, truncated }: { diff: string; truncated: boolean | undefined }) {
  const lines = useMemo(() => parseDiff(diff), [diff]);
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

function NumberedDiff({ diff, truncated, path }: { diff: string; truncated: boolean | undefined; path: string | undefined }) {
  const { rows, segs, split: splitModel } = useModel(diff, path);
  const prefs = usePrefs();
  const split = useContext(SplitAllowed) && prefs.diffView === "split";
  const api = useContext(LineComments);
  // The line that takes a comment: its side and row.
  const [at, setAt] = useState<string | null>(null);
  // The folds the person opened, by view and place.
  const [opened, setOpened] = useState<ReadonlySet<string>>(new Set());
  const open = (k: string) => setOpened((s) => new Set(s).add(k));
  const comments = api !== null && path !== undefined;

  const gutter = (key: string, num: number | null, canComment: boolean): ReactNode => (
    <>
      {comments && canComment && num !== null && (
        <button
          type="button"
          className="diff-add-comment"
          aria-label={`Comment on line ${num}`}
          title="Comment on this line"
          onClick={() => setAt(at === key ? null : key)}
        >
          <Plus className="size-3" />
        </button>
      )}
      {num}
    </>
  );
  const input = (key: string, r: LineRow, num: number | null): ReactNode =>
    comments &&
    at === key &&
    num !== null && (
      <CommentInput
        where={`${path}:${num}`}
        onCancel={() => setAt(null)}
        onSubmit={(text) => {
          api.add(path, num, r.text, text);
          setAt(null);
        }}
      />
    );
  const other = (r: DiffRow, i: number): ReactNode =>
    r.k === "hunk" ? (
      showHunk(r) ? <HunkHead key={i} r={r} /> : null
    ) : r.k === "meta" ? (
      <div key={i} className="diff-line diff-meta" role="row">
        {r.text}
      </div>
    ) : null;
  const tail = truncated === true && <div className="diff-line diff-meta">The diff is cut short.</div>;

  if (split) {
    const items = foldContext(
      splitModel,
      (s) => s.k === "pair" && s.left !== null && s.left === s.right,
      (s) => s.k === "row",
    );
    const cell = (i: number | null, side: "old" | "new", key: string): ReactNode => {
      const r = i === null ? undefined : (rows[i] as LineRow);
      if (r === undefined) {
        return (
          <>
            <span className="diff-gutter is-empty" />
            <span className="diff-code is-empty" />
          </>
        );
      }
      const num = side === "old" ? r.oldNum : r.newNum;
      // The old side takes comments on removed lines; the new side on the rest.
      const canComment = side === "old" ? r.kind === "del" : true;
      return (
        <>
          <span className={`diff-gutter is-${r.kind}`} data-side={side}>
            {gutter(key, num, canComment)}
          </span>
          <span className={`diff-code is-${r.kind}`} data-side={side}>
            <SegLine segs={segs(i as number)} />
          </span>
        </>
      );
    };
    const pairRow = (s: SplitRow, n: string): ReactNode => {
      if (s.k === "row") return <Fragment key={n}>{other(rows[s.i] as DiffRow, s.i)}</Fragment>;
      const lk = `old:${s.left}`;
      const rk = `new:${s.right}`;
      const commented = at === lk ? s.left : at === rk ? s.right : null;
      const r = commented === null ? undefined : (rows[commented] as LineRow);
      return (
        <Fragment key={n}>
          <div className="diff-row diff-pair" role="row" data-line={s.right === null ? undefined : ((rows[s.right] as LineRow).newNum ?? undefined)}>
            {cell(s.left, "old", lk)}
            {cell(s.right, "new", rk)}
          </div>
          {r !== undefined && input(at as string, r, at === lk ? r.oldNum : r.newNum)}
        </Fragment>
      );
    };
    return (
      <div className="diff diff-numbered diff-split syn-scope" role="table" aria-label="Diff">
        <div className="diff-grid">
          {items.map((it, n) => {
            if (!isFold(it)) return pairRow(it, String(n));
            const k = `s${n}`;
            return opened.has(k) ? (
              <Fragment key={k}>{it.items.map((s, j) => pairRow(s, `${n}.${j}`))}</Fragment>
            ) : (
              <FoldRow key={k} n={it.items.length} onOpen={() => open(k)} />
            );
          })}
        </div>
        {tail}
      </div>
    );
  }

  const items = foldContext(
    rows.map((_, i) => i),
    (i) => {
      const r = rows[i];
      return r?.k === "line" && r.kind === "ctx";
    },
    (i) => rows[i]?.k !== "line",
  );
  const lineRow = (i: number): ReactNode => {
    const r = rows[i] as DiffRow;
    if (r.k !== "line") return other(r, i);
    const key = `u:${i}`;
    return (
      <div key={i} role="rowgroup">
        <div className={`diff-row diff-${r.kind}`} role="row" data-line={r.num ?? undefined}>
          <span className="diff-gutter">{gutter(key, r.num, true)}</span>
          <span className="diff-code">
            <SegLine segs={segs(i)} />
          </span>
        </div>
        {input(key, r, r.num)}
      </div>
    );
  };
  return (
    <div className={cn("diff diff-numbered syn-scope", prefs.diffWrap && "diff-wrap")} role="table" aria-label="Diff">
      <div className="diff-body">
        {items.map((it, n) => {
          if (!isFold(it)) return lineRow(it);
          const k = `u${it.items[0] ?? n}`;
          return opened.has(k) ? (
            <Fragment key={k}>{it.items.map(lineRow)}</Fragment>
          ) : (
            <FoldRow key={k} n={it.items.length} onOpen={() => open(k)} />
          );
        })}
      </div>
      {tail}
    </div>
  );
}
