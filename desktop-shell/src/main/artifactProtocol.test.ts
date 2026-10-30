import { describe, expect, test } from "vitest";
import { entryUrl } from "./artifact";
import { ARTIFACT_CSP, ArtifactHosts, BUILT_SCRIPT, respond, type Served, urlOf } from "./artifactProtocol";

const html: Served = {
  kind: "html",
  entrypoint: "index.html",
  files: new Map([
    ["index.html", '<script src="app.js"></script>'],
    ["app.js", "1"],
    ["notes.txt", "x"],
  ]),
};
const react: Served = { kind: "react", entrypoint: "App.tsx", files: new Map(), built: { js: "J", css: "" } };

describe("respond", () => {
  test("serves the entry and sibling files with their own media types", () => {
    expect(respond(html, "/")).toMatchObject({ status: 200, type: "text/html; charset=utf-8" });
    expect(respond(html, "/app.js")).toMatchObject({ status: 200, type: "text/javascript; charset=utf-8" });
  });

  test.each(["/notes.txt", "/missing.js", "/../app.js", "/a/../app.js", "/%2e%2e/app.js", "/a\\b", "/%E0%A4%A"])(
    "refuses %s",
    (p) => {
      expect(respond(html, p).status).toBe(404);
    },
  );

  test("serves a React shell and its build, and only those", () => {
    expect(respond(react, "/").body).toContain(BUILT_SCRIPT);
    expect(respond(react, "/").body).not.toContain("stylesheet");
    expect(respond(react, `/${BUILT_SCRIPT}`).body).toBe("J");
    expect(respond(react, "/App.tsx").status).toBe(404);
  });

  test("unknown content is a 404", () => {
    expect(respond(undefined, "/").status).toBe(404);
  });
});

describe("a nested entry", () => {
  const nested: Served = {
    kind: "html",
    entrypoint: "src/index.html",
    files: new Map([
      ["src/index.html", '<link rel="stylesheet" href="./style.css">'],
      ["src/style.css", "b{}"],
    ]),
  };
  test("the page opens at its own path, so ./style.css resolves beside it", () => {
    expect(entryUrl("h", nested)).toBe("deck-artifact://h/src/index.html");
    expect(respond(nested, "/src/style.css")).toMatchObject({ status: 200, type: "text/css; charset=utf-8" });
    expect(entryUrl("h", react)).toBe("deck-artifact://h/");
  });
});

describe("ArtifactHosts", () => {
  test("a host serves only the revision it was added for, with the CSP", async () => {
    const hosts = new ArtifactHosts();
    const a = hosts.add(html);
    const b = hosts.add(react);
    expect(a).not.toBe(b);
    const ra = hosts.handle(`${urlOf(a)}app.js`);
    expect(ra.status).toBe(200);
    expect(ra.headers.get("content-security-policy")).toBe(ARTIFACT_CSP);
    expect(await ra.text()).toBe("1");
    expect(hosts.handle(`${urlOf(b)}app.js`).status).toBe(404);
    expect(hosts.handle(`deck-artifact://${"0".repeat(32)}/`).status).toBe(404);
    hosts.remove(a);
    expect(hosts.handle(urlOf(a)).status).toBe(404);
  });

  test("another scheme or a malformed URL gets nothing", () => {
    const hosts = new ArtifactHosts();
    const a = hosts.add(html);
    expect(hosts.handle(`https://${a}/`).status).toBe(404);
    expect(hosts.handle("not a url").status).toBe(404);
  });

  test("the CSP forbids network, frames, forms and base URLs", () => {
    expect(ARTIFACT_CSP).toContain("default-src 'none'");
    expect(ARTIFACT_CSP).not.toContain("connect-src");
    for (const d of ["frame-src 'none'", "form-action 'none'", "base-uri 'none'"]) expect(ARTIFACT_CSP).toContain(d);
  });
});
