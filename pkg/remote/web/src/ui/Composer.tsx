import { useEffect, useMemo, useRef, useState } from "preact/hooks";
import { canSend } from "../composer";
import {
  base64Length,
  blockedVideo,
  checkImage,
  checkVideo,
  cleanLabel,
  defaultLabel,
  displayName,
  isVideo,
  MAX_LABEL,
  MAX_VIDEO_BYTES_REMOTE,
  type Outgoing,
  readAsBase64,
  type Source,
  VIDEO_TYPES,
  videoGate,
} from "../images";
import {
  applySuggestion,
  fileSuggestions,
  type Picked,
  pickerAt,
  suggestionHue,
  suggestions,
} from "../mentions";
import type { AgentLine, SessionLead, Theme } from "../protocol";
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
  // The agent every message goes to, and how to change it (the agents
  // sheet). There is no "@" or "$" to address one.
  lead?: SessionLead | undefined;
  onAgents?: () => void;
  // The Deck's features: "video" (and "video_frames") let a video go.
  features?: readonly string[];
}

// An attachment and the object URL of its preview.
interface Attached {
  att: Outgoing;
  url: string;
}

// The pickers over the prompt, as on the desktop: "#" (or "@" before a path)
// the thread's changed files. Agents are not addressed: the lead answers.
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
  lead,
  onAgents,
  features = [],
}: ComposerProps) {
  const controller = useController();
  const [text, setText] = useState("");
  const [images, setImages] = useState<Attached[]>([]);
  // The attachment whose label is being edited, and the text so far.
  const [renaming, setRenaming] = useState<{ i: number; text: string } | null>(null);
  const gate = videoGate(features, lead);
  const videoOn = gate.available;
  const blocked = blockedVideo(
    images.map((a) => a.att),
    gate,
  );
  // Previews hold object URLs until they go.
  const shown = useRef(images);
  shown.current = images;
  useEffect(
    () => () => {
      for (const a of shown.current) URL.revokeObjectURL(a.url);
    },
    [],
  );
  const [sending, setSending] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const areaRef = useRef<HTMLTextAreaElement>(null);

  const [picker, setPicker] = useState<Picked | null>(null);
  const [pick, setPick] = useState(0);
  // The thread's changed files, asked for when "#" first opens in it.
  const [paths, setPaths] = useState<{ session: string; paths: string[] } | null>(null);
  const asked = useRef<string | null>(null);

  const attachedBytes = images.reduce((n, a) => n + (a.att.data_b64?.length ?? 0), 0);

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
    await addFiles(files, "file");
  };

  // Images, and one video per message where the Deck takes video, each
  // labelled as it lands: a pasted image "Screenshot 1", a file its name.
  // A video is uploaded when the message is sent.
  const addFiles = async (files: File[], source: Source) => {
    setProblem(null);
    let total = attachedBytes;
    let videos = images.filter((a) => isVideo(a.att.mime)).length;
    const taken = images.map((a) => a.att.label ?? "");
    const added: Attached[] = [];
    for (const f of files) {
      if (isVideo(f.type)) {
        const check = videoOn
          ? checkVideo(f, MAX_VIDEO_BYTES_REMOTE)
          : { ok: false as const, reason: "This Deck takes no video." };
        if (!check.ok) {
          setProblem(check.reason);
          continue;
        }
        if (videos > 0) {
          setProblem("One video per message.");
          continue;
        }
        videos++;
        const label = defaultLabel("video", source, f.name, taken);
        taken.push(label);
        added.push({
          att: { name: displayName(f.name || "video.mp4"), mime: f.type, label, file: f },
          url: URL.createObjectURL(f),
        });
        continue;
      }
      const check = checkImage(f, total);
      if (!check.ok) {
        setProblem(check.reason);
        continue;
      }
      try {
        const data = await readAsBase64(f);
        const label = defaultLabel("image", source, f.name, taken);
        taken.push(label);
        added.push({
          att: { name: displayName(f.name), mime: f.type, label, data_b64: data },
          url: URL.createObjectURL(f),
        });
        total += base64Length(f.size);
      } catch {
        setProblem(`Could not read ${displayName(f.name)}.`);
      }
    }
    if (added.length > 0) setImages((cur) => [...cur, ...added]);
  };

  const change = (i: number, patch: Partial<Outgoing>) =>
    setImages((cur) => cur.map((a, j) => (j === i ? { ...a, att: { ...a.att, ...patch } } : a)));
  const remove = (i: number) =>
    setImages((cur) =>
      cur.filter((a, j) => {
        if (j === i) URL.revokeObjectURL(a.url);
        return j !== i;
      }),
    );
  const saveLabel = () => {
    if (renaming === null) return;
    const label = cleanLabel(renaming.text);
    if (label !== "") change(renaming.i, { label });
    setRenaming(null);
  };

  const send = async () => {
    if (sending || !online || !canSend(text, images.length)) return;
    if (blocked) {
      setProblem(`${gate.reason}: remove the video or send it as frames.`);
      return;
    }
    setSending(true);
    setProblem(null);
    const ok = await controller.send(
      sessionId,
      text.trim(),
      images.map((a) => a.att),
    );
    setSending(false);
    if (ok) {
      setText("");
      for (const a of images) URL.revokeObjectURL(a.url);
      setImages([]);
      setRenaming(null);
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
      {lead !== undefined ? (
        <div class="chips">
          <button
            type="button"
            class="chip lead-chip"
            style={{ borderColor: kindColour(lead.kind) }}
            aria-label={`Agent: ${lead.profile || lead.kind}${lead.slug || lead.model ? `, ${lead.slug || lead.model}` : ""}. Change it`}
            onClick={onAgents}
          >
            <span class="ellipsis">
              {lead.profile || lead.kind}
              {lead.slug || lead.model ? (
                <span class="lead-model"> · {lead.slug || lead.model}</span>
              ) : null}
            </span>
          </button>
        </div>
      ) : null}
      {images.length > 0 ? (
        <ul class="attachments" aria-label="Attachments">
          {images.map(({ att, url }, i) => {
            const video = isVideo(att.mime);
            const held = video && !gate.ok && att.frames !== true;
            const label = att.label ?? att.name;
            return (
              <li
                key={url}
                class={`attachment ${held ? "held" : ""}`}
                data-kind={video ? "video" : "image"}
              >
                <span class="attachment-thumb" aria-hidden="true">
                  {video ? (
                    <>
                      <video src={`${url}#t=0.1`} muted preload="metadata" playsInline />
                      <span class="attachment-film">▶</span>
                    </>
                  ) : (
                    <img src={url} alt="" />
                  )}
                </span>
                <span class="attachment-body">
                  {renaming?.i === i ? (
                    <input
                      class="attachment-input"
                      aria-label="Attachment label"
                      value={renaming.text}
                      maxLength={MAX_LABEL}
                      enterkeyhint="done"
                      ref={(el) => el?.focus()}
                      onInput={(e) => setRenaming({ i, text: e.currentTarget.value })}
                      onKeyDown={(e) => {
                        if (e.key === "Enter") {
                          e.preventDefault();
                          saveLabel();
                        } else if (e.key === "Escape") {
                          e.preventDefault();
                          setRenaming(null);
                        }
                      }}
                      onBlur={saveLabel}
                    />
                  ) : (
                    <button
                      type="button"
                      class="attachment-label ellipsis"
                      aria-label={`Rename ${label}`}
                      onClick={() => setRenaming({ i, text: label })}
                    >
                      {label}
                    </button>
                  )}
                  {video && !gate.ok ? (
                    <>
                      <span class="attachment-note">
                        {att.frames === true ? "Goes as still frames" : gate.reason}
                      </span>
                      {/* The way out: up to 8 stills the engine takes with ffmpeg. */}
                      <button
                        type="button"
                        class="attachment-frames"
                        aria-pressed={att.frames === true}
                        disabled={att.frames !== true && !gate.frames}
                        onClick={() => change(i, { frames: att.frames !== true })}
                      >
                        {att.frames === true
                          ? "Send the video instead"
                          : gate.frames
                            ? "Send as frames"
                            : gate.framesReason}
                      </button>
                    </>
                  ) : null}
                </span>
                <button
                  type="button"
                  class="btn ghost small"
                  aria-label={`Remove ${att.name}`}
                  onClick={() => remove(i)}
                >
                  Remove
                </button>
              </li>
            );
          })}
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
                  style={{ color: suggestionHue(s, kindColour) }}
                >
                  {s.kind !== null ? kindMark(theme, s.kind) || "●" : s.label === "all" ? "∗" : "◇"}
                </span>
              ) : null}
              <span class="suggest-name" style={{ color: suggestionHue(s, kindColour) }}>
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
          accept={`image/png,image/jpeg,image/gif,image/webp${videoOn ? `,${[...VIDEO_TYPES].join(",")}` : ""}`}
          multiple
          hidden
          onChange={(e) => void onFiles(e)}
        />
        <button
          type="button"
          class="btn icon"
          aria-label={videoOn ? "Attach images or a video" : "Attach image"}
          onClick={() => fileRef.current?.click()}
        >
          +
        </button>
        <textarea
          ref={areaRef}
          rows={1}
          value={text}
          placeholder={
            busy
              ? "Queue a message"
              : `Message ${lead?.profile || lead?.kind || "the agent"}, # a file`
          }
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
          onPaste={(e) => {
            const pasted = Array.from(e.clipboardData?.files ?? []);
            if (!pasted.some((f) => f.type.startsWith("image/") || isVideo(f.type))) return;
            e.preventDefault();
            void addFiles(pasted, "paste");
          }}
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
          disabled={sending || !online || !canSend(text, images.length) || blocked}
        >
          {sending ? "Sending" : "Send"}
        </button>
      </div>
    </form>
  );
}
