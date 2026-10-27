import { fileSuggestions, type Mention, type Suggestion } from "@web/mentions";
import { SLASH_COMMANDS } from "./slash";

// The composer's pickers. The triggers and the lists of agents, people and
// files are shared with the phone (pkg/remote/web/src/mentions.ts): "@"
// names agents and people, "$" agents, "#" (or "@" before a path) files,
// and "/" a command, at the start of the message.
export {
  applySuggestion,
  contextAt,
  fileAt,
  fileSuggestions,
  type Mention,
  matchPaths,
  mentionAt,
  nameAt,
  pathLike,
  type Person,
  type Picked,
  type PickerType,
  pickerAt,
  retiredForm,
  type ProfileRef,
  type Suggestion,
  suggestionHue,
  suggestions,
} from "@web/mentions";

// The "/command" being typed at the start of the message: the slash
// commands run only as a whole message (slash.ts).
export function slashAt(text: string, caret: number): Mention | null {
  const m = /^\/([\w-]*)$/.exec(text.slice(0, caret));
  return m === null ? null : { start: 0, query: m[1] ?? "" };
}

export function slashSuggestions(query: string): Suggestion[] {
  const q = query.toLowerCase();
  return SLASH_COMMANDS.filter((c) => c.name.startsWith(q)).map((c) => ({
    insert: `/${c.name}`,
    label: c.args === "" ? `/${c.name}` : `/${c.name} ${c.args}`,
    detail: c.detail,
    kind: null,
  }));
}

// How many files "#" asks Deck for, and how long it waits for the typing to stop.
export const FILES_LIMIT = 20;
export const FILES_DEBOUNCE_MS = 120;

// The room's changed files that match first, then the tree's other files
// (ListFiles, already matched by Deck), each once.
export function mergeFiles(query: string, changed: readonly string[], listed: readonly string[]): Suggestion[] {
  const first = fileSuggestions(query, changed);
  const seen = new Set(first.map((s) => s.label));
  const rest = listed.filter((p) => !seen.has(p)).map((p) => ({ insert: `\`${p}\``, label: p, detail: "", kind: null }));
  return [...first, ...rest].slice(0, FILES_LIMIT);
}

// Asks list for the files of a query once the typing stops for
// FILES_DEBOUNCE_MS. Only the latest query's answer reaches found.
export function fileLister(
  list: (query: string) => Promise<readonly string[] | null>,
  found: (query: string, paths: readonly string[]) => void,
): { ask: (query: string) => void; stop: () => void } {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let latest = 0;
  return {
    ask: (query) => {
      clearTimeout(timer);
      const n = ++latest;
      timer = setTimeout(() => {
        void list(query).then((paths) => {
          if (n === latest && paths !== null) found(query, paths);
        });
      }, FILES_DEBOUNCE_MS);
    },
    stop: () => {
      clearTimeout(timer);
      latest++;
    },
  };
}
