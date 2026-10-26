import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { activeAgent, applyAgentChip, canSend } from "../composer";
import { base64Length, checkImage, displayName, readAsBase64 } from "../images";
import { applySuggestion, fileSuggestions, type Picked, pickerAt, suggestions } from "../mentions";
import type { AgentLine, Image, Theme } from "../protocol";
import { kindColour, kindMark } from "../theme";
import { useController } from "./hooks";

interface ComposerProps {
  sessionId: string;
  roster: AgentLine[];
  busy: boolean;
  online: boolean;
  // The agent kinds the Deck runs, for "@" and "$".
  kinds?: string[];
  theme?: Theme | null;
  // Whether the Deck lists the thread's changed files, for "#".
  files?: boolean;
}

// The pickers over the prompt, as on the desktop: "@" agents and people,
// "$" agents, "#" (or "@" before a path) the thread's changed files.
const PICKER_LABEL = { "@": "Agents and people", $: "Agents", "#": "Files" } as const;
const PICKER_ROWS = 6;

export function Composer({
  sessionId,
  roster,
  busy,
  online,
  kinds = [],
  theme = null,
  files = false,
}: ComposerProps) {
  const controller = useController();
  const [text, setText] = useState("");
  const [images, setImages] = useState<Image[]>([]);
  const [sending, setSending] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const areaRef = useRef<HTMLTextAreaElement>(null);

  const [picker, setPicker] = useState<Picked | null>(null);
  const [pick, setPick] = useState(0);
  // The thread's changed files, asked for when "#" first opens in it.
  const [paths, setPaths] = useState<{ session: string; paths: string[] } | null>(null);
  const asked = useRef<string | null>(null);

  const attachedBytes = images.reduce((n, i) => n + (i.data_b64?.length ?? 0), 0);
  const current = activeAgent(text);

  const options = useMemo(() => {
    if (picker === null) return [];
    if (picker.type === "#") {
      const changed = paths?.session === sessionId ? paths.paths : [];
      return fileSuggestions(picker.m.query, changed).slice(0, PICKER_ROWS);
    }
    return suggestions(picker.m.query, roster, kinds, { sigil: picker.type }).slice(0, PICKER_ROWS);
  }, [picker, paths, sessionId, roster, kinds]);

  // A thread change closes the picker.
  useEffect(() => setPicker(null), [sessionId]);
  const closing = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  useEffect(() => () => clearTimeout(closing.current), []);

  const resize = () => {
    const el = areaRef.current;
    if (el === null) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`;
  };

  const track = (el: HTMLTextAreaElement) => {
    const next = pickerAt(el.value, el.selectionStart, files);
    if (next?.type === "#" && asked.current !== sessionId) {
      const session = sessionId;
      asked.current = session;
      void controller
        .changes(session)
        .then((f) => setPaths({ session, paths: f?.map((x) => x.path) ?? [] }));
    }
    setPicker(next);
    setPick(0);
  };

  const accept = (i: number) => {
    const s = options[i];
    if (s === undefined || picker === null) return;
    const next = applySuggestion(text, picker.m, s);
    setText(next.text);
    setPicker(null);
    requestAnimationFrame(() => {
      const el = areaRef.current;
      if (el === null) return;
      el.focus();
      el.setSelectionRange(next.caret, next.caret);
      resize();
    });
  };

  const onFiles = async (e: Event) => {
    const input = e.currentTarget;
    if (!(input instanceof HTMLInputElement) || input.files === null) return;
    const files = Array.from(input.files);
    input.value = "";
    setProblem(null);
    let total = attachedBytes;
    const added: Image[] = [];
    for (const f of files) {
      const check = checkImage(f, total);
      if (!check.ok) {
        setProblem(check.reason);
        continue;
      }
      try {
        const data = await readAsBase64(f);
        added.push({ name: displayName(f.name), mime: f.type, data_b64: data });
        total += base64Length(f.size);
      } catch {
        setProblem(`Could not read ${displayName(f.name)}.`);
      }
    }
    if (added.length > 0) setImages((cur) => [...cur, ...added]);
  };

  const send = async () => {
    if (sending || !online || !canSend(text, images.length)) return;
    setSending(true);
    setProblem(null);
    const ok = await controller.send(sessionId, text.trim(), images);
    setSending(false);
    if (ok) {
      setText("");
      setImages([]);
      setPicker(null);
      requestAnimationFrame(resize);
    }
  };

  return (
    <form
      class="composer"
      onSubmit={(e) => {
        e.preventDefault();
        void send();
      }}
    >
      {roster.length > 0 ? (
        <fieldset class="chips">
          <legend class="sr-only">Address an agent</legend>
          {roster.map((a) => (
            <button
              key={a.id}
              type="button"
              class={`chip ${current === a.name ? "active" : ""}`}
              aria-pressed={current === a.name}
              style={current === a.name ? {} : { borderColor: kindColour(a.kind) }}
              onClick={() => {
                setText((t) => applyAgentChip(t, a.name));
                areaRef.current?.focus();
              }}
            >
              ${a.name}
            </button>
          ))}
        </fieldset>
      ) : null}
      {images.length > 0 ? (
        <ul class="attachments" aria-label="Attached images">
          {images.map((img, i) => (
            <li key={i} class="attachment">
              <span class="ellipsis">{img.name}</span>
              <button
                type="button"
                class="btn ghost small"
                aria-label={`Remove ${img.name}`}
                onClick={() => setImages((cur) => cur.filter((_, j) => j !== i))}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      ) : null}
      {picker !== null && options.length > 0 ? (
        <div class="suggest" role="listbox" aria-label={PICKER_LABEL[picker.type]}>
          {options.map((s, i) => (
            <button
              key={s.insert}
              type="button"
              role="option"
              tabIndex={-1}
              aria-selected={i === pick}
              class={`suggest-row ${i === pick ? "on" : ""}`}
              // The prompt keeps the focus, so the keyboard stays up.
              onPointerDown={(e) => e.preventDefault()}
              onClick={() => accept(i)}
            >
              {picker.type !== "#" ? (
                <span
                  class="suggest-mark"
                  aria-hidden="true"
                  style={s.kind === null ? {} : { color: kindColour(s.kind) }}
                >
                  {s.kind !== null ? kindMark(theme, s.kind) || "●" : s.label === "all" ? "∗" : "◇"}
                </span>
              ) : null}
              <span
                class="suggest-name"
                style={s.kind === null ? {} : { color: kindColour(s.kind) }}
              >
                {picker.type === "$" ? s.insert : s.label}
              </span>
              <span class="suggest-detail">{s.detail}</span>
            </button>
          ))}
        </div>
      ) : null}
      {problem !== null ? (
        <p class="error-text" role="alert">
          {problem}
        </p>
      ) : null}
      <div class="composer-row">
        <input
          ref={fileRef}
          type="file"
          accept="image/png,image/jpeg,image/gif,image/webp"
          multiple
          hidden
          onChange={(e) => void onFiles(e)}
        />
        <button
          type="button"
          class="btn icon"
          aria-label="Attach image"
          onClick={() => fileRef.current?.click()}
        >
          +
        </button>
        <textarea
          ref={areaRef}
          rows={1}
          value={text}
          placeholder={busy ? "Queue a message" : "Message, @ an agent, # a file"}
          aria-label="Message"
          enterkeyhint="send"
          onInput={(e) => {
            setText(e.currentTarget.value);
            resize();
            track(e.currentTarget);
          }}
          onClick={(e) => track(e.currentTarget)}
          // A tap on a row may blur the prompt first (iOS moves the focus
          // on the tap); the picker stays long enough for the tap to land.
          onBlur={() => {
            closing.current = setTimeout(() => setPicker(null), 250);
          }}
          onFocus={() => clearTimeout(closing.current)}
          onKeyDown={(e) => {
            if (options.length > 0) {
              if (e.key === "ArrowDown" || e.key === "ArrowUp") {
                e.preventDefault();
                const step = e.key === "ArrowDown" ? 1 : -1;
                setPick((p) => (p + step + options.length) % options.length);
                return;
              }
              if (e.key === "Enter" || e.key === "Tab") {
                e.preventDefault();
                accept(pick);
                return;
              }
              if (e.key === "Escape") {
                e.preventDefault();
                setPicker(null);
                return;
              }
            }
            if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
              e.preventDefault();
              void send();
            }
          }}
        />
        {busy ? (
          <button
            type="button"
            class="btn danger"
            onClick={() => void controller.cancel(sessionId)}
          >
            Cancel
          </button>
        ) : null}
        <button
          type="submit"
          class="btn primary"
          disabled={sending || !online || !canSend(text, images.length)}
        >
          {sending ? "Sending" : "Send"}
        </button>
      </div>
    </form>
  );
}
