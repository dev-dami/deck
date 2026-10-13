export type Route =
  | { name: "sessions" }
  | { name: "session"; id: string }
  | { name: "changes"; id: string }
  | { name: "devices" };

export function parseRoute(hash: string): Route {
  const parts = hash
    .replace(/^#/, "")
    .split("/")
    .filter((p) => p !== "");
  const [a, b, c] = parts;
  if (a === "s" && b !== undefined) {
    try {
      const id = decodeURIComponent(b);
      return c === "changes" ? { name: "changes", id } : { name: "session", id };
    } catch {
      return { name: "sessions" };
    }
  }
  if (a === "devices") return { name: "devices" };
  return { name: "sessions" };
}

export function formatRoute(r: Route): string {
  switch (r.name) {
    case "sessions":
      return "#/";
    case "session":
      return `#/s/${encodeURIComponent(r.id)}`;
    case "changes":
      return `#/s/${encodeURIComponent(r.id)}/changes`;
    case "devices":
      return "#/devices";
  }
}
