import type { ComponentChildren } from "preact";
import { useMemo } from "preact/hooks";
import { type Block, type Inline, parseBlocks } from "../markdown";

// The node tree from markdown.ts becomes elements with text children only;
// nothing here ever turns a string into markup. Links are http(s) only and
// open in a new tab without a referrer or opener (§6).
function renderInline(n: Inline, i: number): ComponentChildren {
  switch (n.k) {
    case "text":
      return n.text;
    case "code":
      return <code key={i}>{n.text}</code>;
    case "strong":
      return (
        <strong key={i}>
          <InlineNodes nodes={n.children} />
        </strong>
      );
    case "em":
      return (
        <em key={i}>
          <InlineNodes nodes={n.children} />
        </em>
      );
    case "link":
      return (
        <a key={i} href={n.href} target="_blank" rel="noopener noreferrer">
          {n.text}
        </a>
      );
  }
}

function InlineNodes({ nodes }: { nodes: Inline[] }) {
  return <>{nodes.map((n, i) => renderInline(n, i))}</>;
}

function renderBlock(b: Block, i: number): ComponentChildren {
  switch (b.k) {
    case "p":
      return (
        <p key={i}>
          <InlineNodes nodes={b.inline} />
        </p>
      );
    case "h":
      return (
        <p key={i} class={`md-h md-h${Math.min(b.level, 4)}`}>
          <InlineNodes nodes={b.inline} />
        </p>
      );
    case "code":
      return (
        <pre key={i} class="codeblock" data-lang={b.lang}>
          <code>{b.text}</code>
        </pre>
      );
    case "list": {
      const items = b.items.map((it, j) => (
        <li key={j} class={`depth${it.depth}`}>
          <InlineNodes nodes={it.inline} />
        </li>
      ));
      return b.ordered ? <ol key={i}>{items}</ol> : <ul key={i}>{items}</ul>;
    }
    case "quote":
      return (
        <blockquote key={i}>
          <BlockNodes blocks={b.blocks} />
        </blockquote>
      );
    case "table":
      return (
        <div key={i} class="md-table">
          <table>
            <thead>
              <tr>
                {b.head.map((c, j) => (
                  <th key={j} data-align={b.align[j] || undefined}>
                    <InlineNodes nodes={c} />
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {b.rows.map((r, j) => (
                <tr key={j}>
                  {r.map((c, k) => (
                    <td key={k} data-align={b.align[k] || undefined}>
                      <InlineNodes nodes={c} />
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

function BlockNodes({ blocks }: { blocks: Block[] }) {
  return <>{blocks.map((b, i) => renderBlock(b, i))}</>;
}

export function Markdown({ text }: { text: string }) {
  const blocks = useMemo(() => parseBlocks(text), [text]);
  return (
    <div class="md">
      <BlockNodes blocks={blocks} />
    </div>
  );
}
