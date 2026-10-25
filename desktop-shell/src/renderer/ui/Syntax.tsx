import { Fragment, useMemo } from "react";
import { expandTabs, highlightCode, langOfFence, type Seg } from "../highlight";

// A line's pieces as spans with their colour class; a changed word adds
// diff-word. Text stays text: React escapes it.
export function SegLine({ segs }: { segs: readonly Seg[] }) {
  let col = 0;
  return (
    <>
      {segs.map((s, i) => {
        const [text, next] = expandTabs(s.text, col);
        col = next;
        const cls = s.mark ? `${s.cls} diff-word`.trim() : s.cls;
        return cls === "" ? <Fragment key={i}>{text}</Fragment> : <span key={i} className={cls}>{text}</span>;
      })}
    </>
  );
}

// A fenced code block from agent markdown, coloured when Deck knows its
// language.
export function CodeBlock({ text, lang }: { text: string; lang: string }) {
  const lines = useMemo(() => highlightCode(text, langOfFence(lang)), [text, lang]);
  return (
    <pre className="codeblock syn-scope" data-lang={lang}>
      <code>
        {lines === null
          ? text
          : lines.map((l, i) => (
              <Fragment key={i}>
                {i > 0 && "\n"}
                <SegLine segs={l} />
              </Fragment>
            ))}
      </code>
    </pre>
  );
}
