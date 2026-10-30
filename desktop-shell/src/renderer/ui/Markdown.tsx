import { type Block, type Inline, parseBlocks } from "@web/markdown";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import { Check, Copy } from "./icons";
import { CodeBlock } from "./Syntax";

// A fenced block as a card: the code, and in its corner the language and a
// copy button that shows on hover.
function CodeCard({ text, lang }: { text: string; lang: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <div className="md-code group/code relative">
      <CodeBlock text={text} lang={lang} />
      {lang !== "" && (
        <span className="pointer-events-none absolute top-2.5 right-3.5 font-mono text-3xs text-faint transition-opacity group-focus-within/code:opacity-0 group-hover/code:opacity-0">
          {lang}
        </span>
      )}
      <button
        type="button"
        className="absolute top-1.5 right-1.5 grid size-7 cursor-pointer place-items-center rounded-md text-muted-foreground opacity-0 outline-none transition-opacity hover:bg-hover hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring group-hover/code:opacity-100"
        aria-label={copied ? "Copied" : "Copy code"}
        title="Copy"
        onClick={() => {
          void navigator.clipboard
            .writeText(text)
            .then(() => setCopied(true))
            .catch(() => {});
        }}
      >
        {copied ? <Check className="size-3.5" weight="bold" /> : <Copy className="size-3.5" />}
      </button>
    </div>
  );
}

// The node tree from the shared markdown parser becomes elements with text
// children only; no string from Deck ever becomes markup. Links are http(s)
// only and open in the browser (main denies new windows and hands the URL
// to the system).
function renderInline(n: Inline, i: number): ReactNode {
  switch (n.k) {
    case "text":
      return n.text;
    case "code":
      return <code key={i}>{n.text}</code>;
    case "strong":
      return <strong key={i}>{n.children.map(renderInline)}</strong>;
    case "em":
      return <em key={i}>{n.children.map(renderInline)}</em>;
    case "link":
      return (
        <a key={i} href={n.href} target="_blank" rel="noopener noreferrer">
          {n.text}
        </a>
      );
  }
}

function renderBlock(b: Block, i: number): ReactNode {
  switch (b.k) {
    case "p":
      return <p key={i}>{b.inline.map(renderInline)}</p>;
    case "h":
      return (
        <p key={i} className={`md-h md-h${Math.min(b.level, 4)}`}>
          {b.inline.map(renderInline)}
        </p>
      );
    case "code":
      return <CodeCard key={i} text={b.text} lang={b.lang} />;
    case "list": {
      const items = b.items.map((it, j) => (
        <li key={j} className={`depth${it.depth}`}>
          {it.inline.map(renderInline)}
        </li>
      ));
      return b.ordered ? <ol key={i}>{items}</ol> : <ul key={i}>{items}</ul>;
    }
    case "quote":
      return <blockquote key={i}>{b.blocks.map(renderBlock)}</blockquote>;
    case "table":
      // A table scrolls in its own box (.md-table), never the timeline.
      return (
        <div key={i} className="md-table">
          <table>
            <thead>
              <tr>
                {b.head.map((c, j) => (
                  <th key={j} data-align={b.align[j] || undefined}>
                    {c.map(renderInline)}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {b.rows.map((r, j) => (
                <tr key={j}>
                  {r.map((c, k) => (
                    <td key={k} data-align={b.align[k] || undefined}>
                      {c.map(renderInline)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      );
    case "hr":
      return <hr key={i} />;
  }
}

export function Markdown({ text }: { text: string }) {
  const blocks = useMemo(() => parseBlocks(text), [text]);
  return <div className="md">{blocks.map(renderBlock)}</div>;
}
