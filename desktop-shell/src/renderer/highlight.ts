import "./prismManual";
import Prism from "prismjs/components/prism-core";
// The grammars, in the order they need each other. Static imports, so the
// bundle carries them and nothing loads at run time.
import "prismjs/components/prism-markup";
import "prismjs/components/prism-css";
import "prismjs/components/prism-clike";
import "prismjs/components/prism-javascript";
import "prismjs/components/prism-typescript";
import "prismjs/components/prism-jsx";
import "prismjs/components/prism-tsx";
import "prismjs/components/prism-go";
import "prismjs/components/prism-python";
import "prismjs/components/prism-rust";
import "prismjs/components/prism-json";
import "prismjs/components/prism-yaml";
import "prismjs/components/prism-toml";
import "prismjs/components/prism-bash";
import "prismjs/components/prism-markdown";
import "prismjs/components/prism-sql";
import "prismjs/components/prism-diff";
import "prismjs/components/prism-docker";
import "prismjs/components/prism-java";
import "prismjs/components/prism-c";
import "prismjs/components/prism-cpp";
import "prismjs/components/prism-ruby";
import "prismjs/components/prism-swift";
import "prismjs/components/prism-kotlin";
import type { Token, TokenStream } from "prismjs";
import type { DiffRow } from "./diffRows";
import type { Span } from "./wordDiff";

// Syntax colours as tokens, never markup: Prism splits the text into a
// token stream, and each piece becomes a span with a class (Syntax.tsx).

// A piece of a line: its text, its colour class (syn-*, or "" for none)
// and whether it is a changed word.
export interface Seg {
  text: string;
  cls: string;
  mark: boolean;
}

// The languages Deck colours, by Prism's name, with the name a file header
// shows.
const NAMES: Record<string, string> = {
  typescript: "TypeScript",
  tsx: "TSX",
  javascript: "JavaScript",
  jsx: "JSX",
  go: "Go",
  python: "Python",
  rust: "Rust",
  json: "JSON",
  yaml: "YAML",
  toml: "TOML",
  css: "CSS",
  html: "HTML",
  xml: "XML",
  bash: "Shell",
  markdown: "Markdown",
  sql: "SQL",
  diff: "Diff",
  docker: "Dockerfile",
  java: "Java",
  c: "C",
  cpp: "C++",
  ruby: "Ruby",
  swift: "Swift",
  kotlin: "Kotlin",
};

const BY_EXT: Record<string, string> = {
  ts: "typescript",
  mts: "typescript",
  cts: "typescript",
  tsx: "tsx",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  jsx: "jsx",
  go: "go",
  py: "python",
  pyi: "python",
  rs: "rust",
  json: "json",
  jsonc: "json",
  yaml: "yaml",
  yml: "yaml",
  toml: "toml",
  css: "css",
  html: "html",
  htm: "html",
  vue: "html",
  svelte: "html",
  xml: "xml",
  svg: "xml",
  plist: "xml",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  md: "markdown",
  markdown: "markdown",
  sql: "sql",
  diff: "diff",
  patch: "diff",
  dockerfile: "docker",
  java: "java",
  c: "c",
  h: "c",
  cc: "cpp",
  cpp: "cpp",
  cxx: "cpp",
  hpp: "cpp",
  hh: "cpp",
  hxx: "cpp",
  rb: "ruby",
  swift: "swift",
  kt: "kotlin",
  kts: "kotlin",
};

const BY_NAME: Record<string, string> = {
  dockerfile: "docker",
  containerfile: "docker",
  gemfile: "ruby",
  rakefile: "ruby",
  ".bashrc": "bash",
  ".zshrc": "bash",
  ".profile": "bash",
};

// The language of a file, from its name. Null for one Deck does not colour.
export function langOf(path: string): string | null {
  const base = (path.split(/[\\/]/).pop() ?? "").toLowerCase();
  const named = BY_NAME[base];
  if (named !== undefined) return named;
  if (base.startsWith("dockerfile.")) return "docker";
  const dot = base.lastIndexOf(".");
  if (dot <= 0) return null;
  return BY_EXT[base.slice(dot + 1)] ?? null;
}

const FENCE: Record<string, string> = {
  ...BY_EXT,
  typescript: "typescript",
  javascript: "javascript",
  golang: "go",
  python: "python",
  rust: "rust",
  shell: "bash",
  console: "bash",
  docker: "docker",
  "c++": "cpp",
  ruby: "ruby",
  kotlin: "kotlin",
  markup: "html",
};

// The language a fenced code block names after its ```.
export function langOfFence(info: string): string | null {
  const word = info.trim().split(/\s+/)[0]?.toLowerCase() ?? "";
  return FENCE[word] ?? null;
}

export function langName(lang: string): string {
  return NAMES[lang] ?? lang;
}

// Prism's token types and aliases, by the colour class they take. Null
// starts over, so the code inside a string's ${…} is not a string.
const CLASS: Record<string, string | null> = {
  keyword: "keyword",
  atrule: "keyword",
  rule: "keyword",
  important: "keyword",
  directive: "keyword",
  macro: "keyword",
  title: "keyword",
  "control-flow": "keyword",
  module: "keyword",
  string: "string",
  char: "string",
  "template-string": "string",
  "triple-quoted-string": "string",
  "attr-value": "string",
  regex: "string",
  url: "string",
  inserted: "string",
  comment: "comment",
  prolog: "comment",
  doctype: "comment",
  cdata: "comment",
  shebang: "comment",
  number: "number",
  boolean: "number",
  constant: "number",
  null: "number",
  nil: "number",
  symbol: "number",
  datetime: "number",
  function: "function",
  "function-definition": "function",
  "function-name": "function",
  method: "function",
  decorator: "function",
  "class-name": "type",
  builtin: "type",
  namespace: "type",
  lifetime: "type",
  "type-definition": "type",
  punctuation: "punct",
  "interpolation-punctuation": "punct",
  variable: "variable",
  parameter: "variable",
  property: "attr",
  "attr-name": "attr",
  attribute: "attr",
  annotation: "attr",
  key: "attr",
  tag: "tag",
  selector: "tag",
  deleted: "tag",
  operator: "operator",
  entity: "operator",
  interpolation: null,
  "string-interpolation": null,
};

function classOf(t: Token, parent: string): string {
  const names = [t.type, ...(Array.isArray(t.alias) ? t.alias : t.alias === undefined ? [] : [t.alias])];
  for (const n of names) {
    const c = CLASS[n];
    if (c === null) return "";
    if (c !== undefined) return `syn-${c}`;
  }
  return parent;
}

function flatten(stream: TokenStream, cls: string, out: Seg[]): void {
  if (typeof stream === "string") {
    if (stream !== "") out.push({ text: stream, cls, mark: false });
    return;
  }
  if (Array.isArray(stream)) {
    for (const s of stream) flatten(s, cls, out);
    return;
  }
  flatten(stream.content, classOf(stream, cls), out);
}

// Past these a file or a line goes plain: colour is not worth the time.
export const MAX_ROWS = 5000;
export const MAX_LINE = 1000;

// The text's lines as coloured pieces. The whole text is tokenized at once
// and split at newlines, so a line inside a block comment or a template
// string still reads as one. Null for a language Deck does not colour.
export function tokenLines(text: string, lang: string | null): Seg[][] | null {
  const grammar = lang === null ? undefined : Prism.languages[lang];
  if (grammar === undefined) return null;
  const segs: Seg[] = [];
  flatten(Prism.tokenize(text, grammar), "", segs);
  const lines: Seg[][] = [[]];
  for (const s of segs) {
    const parts = s.text.split("\n");
    parts.forEach((p, i) => {
      if (i > 0) lines.push([]);
      if (p !== "") lines[lines.length - 1]?.push({ text: p, cls: s.cls, mark: false });
    });
  }
  return lines;
}

// A fenced code block's lines, or null to show it plain.
export function highlightCode(text: string, lang: string | null): Seg[][] | null {
  const lines = text.split("\n");
  if (lines.length > MAX_ROWS || lines.some((l) => l.length > MAX_LINE)) return null;
  return tokenLines(text, lang);
}

// The coloured pieces of each line row of a diff (null for the rest). Each
// hunk's old text (context and removed lines) and new text (context and
// added lines) are tokenized whole, so the state of a comment or a string
// carries from line to line.
export function highlightRows(rows: readonly DiffRow[], lang: string | null): (Seg[] | null)[] {
  const out: (Seg[] | null)[] = rows.map(() => null);
  if (lang === null || rows.length > MAX_ROWS || Prism.languages[lang] === undefined) return out;
  let i = 0;
  while (i < rows.length) {
    if (rows[i]?.k === "hunk") i++;
    const oldRows: number[] = [];
    const newRows: number[] = [];
    for (; i < rows.length && rows[i]?.k !== "hunk"; i++) {
      const r = rows[i];
      if (r?.k !== "line") continue;
      if (r.kind !== "add") oldRows.push(i);
      if (r.kind !== "del") newRows.push(i);
    }
    side(rows, oldRows, lang, out, "del");
    side(rows, newRows, lang, out, "new");
  }
  return out;
}

function side(rows: readonly DiffRow[], ix: number[], lang: string, out: (Seg[] | null)[], take: "del" | "new"): void {
  if (ix.length === 0) return;
  const text = (i: number) => {
    const r = rows[i];
    return r?.k === "line" ? r.text : "";
  };
  // A line past the limit stays plain; an empty line keeps its place.
  const lines = tokenLines(ix.map((i) => (text(i).length > MAX_LINE ? "" : text(i))).join("\n"), lang);
  if (lines === null) return;
  ix.forEach((i, n) => {
    const r = rows[i];
    if (r?.k !== "line" || text(i).length > MAX_LINE) return;
    if ((take === "del") === (r.kind === "del")) out[i] = lines[n] ?? [];
  });
}

// The pieces of a line with the changed spans marked, cut where a span
// starts or ends inside a piece.
export function markSpans(segs: readonly Seg[], spans: readonly Span[]): Seg[] {
  if (spans.length === 0) return [...segs];
  const cuts = new Set<number>();
  for (const [a, b] of spans) {
    cuts.add(a);
    cuts.add(b);
  }
  const inSpan = (at: number) => spans.some(([a, b]) => at >= a && at < b);
  const out: Seg[] = [];
  let at = 0;
  for (const s of segs) {
    let start = 0;
    for (let k = 1; k <= s.text.length; k++) {
      if (k === s.text.length || cuts.has(at + k)) {
        out.push({ text: s.text.slice(start, k), cls: s.cls, mark: inSpan(at + start) });
        start = k;
      }
    }
    at += s.text.length;
  }
  return out;
}

// A line with no colour, as one piece.
export function plain(text: string): Seg[] {
  return text === "" ? [] : [{ text, cls: "", mark: false }];
}

// Tabs show as spaces to the next stop of four, counted from col.
export const TAB = 4;
export function expandTabs(text: string, col: number): [string, number] {
  if (!text.includes("\t")) return [text, col + text.length];
  let out = "";
  let c = col;
  for (const ch of text) {
    if (ch === "\t") {
      const n = TAB - (c % TAB);
      out += " ".repeat(n);
      c += n;
    } else {
      out += ch;
      c++;
    }
  }
  return [out, c];
}
