import { describe, expect, test } from "bun:test";
import { parseBlocks, parseInline, safeHref } from "./markdown";

describe("inline", () => {
  test("plain text stays text", () => {
    expect(parseInline("hello world")).toEqual([{ k: "text", text: "hello world" }]);
  });

  test("code, bold and emphasis", () => {
    expect(parseInline("a `b` **c** *d*")).toEqual([
      { k: "text", text: "a " },
      { k: "code", text: "b" },
      { k: "text", text: " " },
      { k: "strong", children: [{ k: "text", text: "c" }] },
      { k: "text", text: " " },
      { k: "em", children: [{ k: "text", text: "d" }] },
    ]);
  });

  test("snake_case is not emphasis", () => {
    expect(parseInline("use my_var_name here")).toEqual([
      { k: "text", text: "use my_var_name here" },
    ]);
  });

  test("markup characters inside code are literal", () => {
    expect(parseInline("`<b>*x*</b>`")).toEqual([{ k: "code", text: "<b>*x*</b>" }]);
  });

  test("https links become links, javascript links stay text", () => {
    expect(parseInline("[ok](https://example.com/a)")).toEqual([
      { k: "link", href: "https://example.com/a", text: "ok" },
    ]);
    const bad = parseInline("[x](javascript:alert(1))");
    expect(bad.some((n) => n.k === "link")).toBe(false);
  });

  test("html in agent output is just text", () => {
    const nodes = parseInline("<script>alert(1)</script><img src=x onerror=alert(1)>");
    expect(nodes).toEqual([
      { k: "text", text: "<script>alert(1)</script><img src=x onerror=alert(1)>" },
    ]);
  });

  test("unterminated markers do not swallow the rest", () => {
    expect(parseInline("a ** b")).toEqual([{ k: "text", text: "a ** b" }]);
    expect(parseInline("a `b")).toEqual([{ k: "text", text: "a `b" }]);
  });
});

describe("safeHref", () => {
  test("allows only http and https", () => {
    expect(safeHref("https://a.b/c")).toBe("https://a.b/c");
    expect(safeHref("http://a.b/")).toBe("http://a.b/");
    expect(safeHref("javascript:alert(1)")).toBeNull();
    expect(safeHref("data:text/html,x")).toBeNull();
    expect(safeHref("blob:https://a.b/x")).toBeNull();
    expect(safeHref("/relative")).toBeNull();
    expect(safeHref("")).toBeNull();
  });
});

describe("blocks", () => {
  test("paragraphs, headings and rules", () => {
    const b = parseBlocks("# Title\n\nsome text\nmore\n\n---\n");
    expect(b.map((x) => x.k)).toEqual(["h", "p", "hr"]);
    expect(b[0]).toMatchObject({ k: "h", level: 1 });
  });

  test("fenced code keeps its content verbatim", () => {
    const b = parseBlocks("```ts\nconst a = <b>1</b>;\n# not a heading\n```\nafter");
    expect(b[0]).toEqual({ k: "code", lang: "ts", text: "const a = <b>1</b>;\n# not a heading" });
    expect(b[1]?.k).toBe("p");
  });

  test("an unclosed fence while streaming is still code", () => {
    const b = parseBlocks("```\nstill writing");
    expect(b).toEqual([{ k: "code", lang: "", text: "still writing" }]);
  });

  test("lists, ordered and unordered", () => {
    const b = parseBlocks("- one\n- two\n  cont\n\n1. a\n2. b");
    expect(b).toHaveLength(2);
    expect(b[0]).toMatchObject({ k: "list", ordered: false });
    expect(b[1]).toMatchObject({ k: "list", ordered: true });
    expect(b[0]?.k === "list" && b[0].items).toHaveLength(2);
  });

  test("block quotes nest blocks", () => {
    const b = parseBlocks("> quoted\n> text");
    expect(b[0]?.k).toBe("quote");
  });

  test("handles CRLF and empty input", () => {
    expect(parseBlocks("")).toEqual([]);
    expect(parseBlocks("a\r\n\r\nb").map((x) => x.k)).toEqual(["p", "p"]);
  });
});
