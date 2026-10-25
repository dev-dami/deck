import { type Block, type Inline, parseBlocks } from "@web/markdown";
import { type ReactNode, useMemo } from "react";
import { CodeBlock } from "./Syntax";

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
      return <CodeBlock key={i} text={b.text} lang={b.lang} />;
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
    case "hr":
      return <hr key={i} />;
  }
}

export function Markdown({ text }: { text: string }) {
  const blocks = useMemo(() => parseBlocks(text), [text]);
  return <div className="md">{blocks.map(renderBlock)}</div>;
}
