// Minimal markdown for agent output. It produces a tree, never HTML: the view
// layer renders nodes as text, so nothing an agent emits can become markup.

export type Inline =
  | { k: "text"; text: string }
  | { k: "code"; text: string }
  | { k: "strong"; children: Inline[] }
  | { k: "em"; children: Inline[] }
  | { k: "link"; href: string; text: string };

export interface ListItem {
  depth: number;
  inline: Inline[];
}

export type Block =
  | { k: "p"; inline: Inline[] }
  | { k: "h"; level: number; inline: Inline[] }
  | { k: "code"; lang: string; text: string }
  | { k: "list"; ordered: boolean; items: ListItem[] }
  | { k: "quote"; blocks: Block[] }
  | { k: "hr" };

// Only absolute http(s) links are clickable; javascript:, data: and friends
// stay inert text.
export function safeHref(raw: string): string | null {
  try {
    const u = new URL(raw);
    return u.protocol === "https:" || u.protocol === "http:" ? u.href : null;
  } catch {
    return null;
  }
}

const MAX_DEPTH = 4;
const LINK_RE = /^\[([^\]\n]+)\]\(([^)\s]+)\)/;

function isWordChar(c: string | undefined): boolean {
  return c !== undefined && /[\p{L}\p{N}]/u.test(c);
}

export function parseInline(s: string, depth = 0): Inline[] {
  const out: Inline[] = [];
  let buf = "";
  const flush = () => {
    if (buf !== "") out.push({ k: "text", text: buf });
    buf = "";
  };
  let i = 0;
  while (i < s.length) {
    const c = s.charAt(i);
    if (c === "`") {
      const j = s.indexOf("`", i + 1);
      if (j > i + 1) {
        flush();
        out.push({ k: "code", text: s.slice(i + 1, j) });
        i = j + 1;
        continue;
      }
    } else if (c === "*" && s.startsWith("**", i) && depth < MAX_DEPTH) {
      const j = s.indexOf("**", i + 2);
      if (j > i + 2) {
        flush();
        out.push({ k: "strong", children: parseInline(s.slice(i + 2, j), depth + 1) });
        i = j + 2;
        continue;
      }
    } else if ((c === "*" || c === "_") && depth < MAX_DEPTH) {
      const j = s.indexOf(c, i + 1);
      const intraWord = c === "_" && (isWordChar(s[i - 1]) || isWordChar(s[j + 1]));
      if (j > i + 1 && !/\s/.test(s.charAt(i + 1)) && !/\s/.test(s.charAt(j - 1)) && !intraWord) {
        flush();
        out.push({ k: "em", children: parseInline(s.slice(i + 1, j), depth + 1) });
        i = j + 1;
        continue;
      }
    } else if (c === "[") {
      const m = LINK_RE.exec(s.slice(i));
      const href = m?.[2] === undefined ? null : safeHref(m[2]);
      if (m !== null && m[1] !== undefined && href !== null) {
        flush();
        out.push({ k: "link", href, text: m[1] });
        i += m[0].length;
        continue;
      }
    }
    buf += c;
    i++;
  }
  flush();
  return out;
}

const FENCE_RE = /^\s{0,3}(`{3,}|~{3,})\s*([^\s`]*)/;
const HEADING_RE = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const HR_RE = /^\s{0,3}([-*_])(\s*\1){2,}\s*$/;
const ITEM_RE = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;
const QUOTE_RE = /^\s{0,3}>\s?(.*)$/;

export function parseBlocks(src: string): Block[] {
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i] ?? "";
    if (line.trim() === "") {
      i++;
      continue;
    }
    const fence = FENCE_RE.exec(line);
    if (fence?.[1] !== undefined) {
      const marker = fence[1];
      const body: string[] = [];
      i++;
      // An unclosed fence is normal while a reply is still streaming.
      while (i < lines.length && !(lines[i] ?? "").trim().startsWith(marker)) {
        body.push(lines[i] ?? "");
        i++;
      }
      i++;
      blocks.push({ k: "code", lang: fence[2] ?? "", text: body.join("\n") });
      continue;
    }
    const heading = HEADING_RE.exec(line);
    if (heading?.[1] !== undefined) {
      blocks.push({ k: "h", level: heading[1].length, inline: parseInline(heading[2] ?? "") });
      i++;
      continue;
    }
    if (HR_RE.test(line)) {
      blocks.push({ k: "hr" });
      i++;
      continue;
    }
    if (QUOTE_RE.test(line)) {
      const inner: string[] = [];
      while (i < lines.length) {
        const q = QUOTE_RE.exec(lines[i] ?? "");
        if (q === null) break;
        inner.push(q[1] ?? "");
        i++;
      }
      blocks.push({ k: "quote", blocks: parseBlocks(inner.join("\n")) });
      continue;
    }
    const item = ITEM_RE.exec(line);
    if (item?.[2] !== undefined) {
      const ordered = /\d/.test(item[2]);
      const items: ListItem[] = [];
      while (i < lines.length) {
        const m = ITEM_RE.exec(lines[i] ?? "");
        if (m?.[2] === undefined || /\d/.test(m[2]) !== ordered) break;
        const depth = Math.min(3, Math.floor((m[1] ?? "").replace(/\t/g, "  ").length / 2));
        let text = m[3] ?? "";
        i++;
        // Indented, non-item lines continue the previous item.
        while (i < lines.length) {
          const next = lines[i] ?? "";
          if (next.trim() === "" || ITEM_RE.test(next) || !/^\s{2,}/.test(next)) break;
          text += `\n${next.trim()}`;
          i++;
        }
        items.push({ depth, inline: parseInline(text) });
      }
      blocks.push({ k: "list", ordered, items });
      continue;
    }
    const para: string[] = [];
    while (i < lines.length) {
      const l = lines[i] ?? "";
      if (
        l.trim() === "" ||
        FENCE_RE.test(l) ||
        HEADING_RE.test(l) ||
        QUOTE_RE.test(l) ||
        ITEM_RE.test(l)
      ) {
        break;
      }
      para.push(l);
      i++;
    }
    blocks.push({ k: "p", inline: parseInline(para.join("\n")) });
  }
  return blocks;
}
