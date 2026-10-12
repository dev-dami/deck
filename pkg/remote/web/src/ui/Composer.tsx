import { useRef, useState } from "preact/hooks";
import { activeAgent, applyAgentChip, canSend } from "../composer";
import { base64Length, checkImage, displayName, readAsBase64 } from "../images";
import type { AgentLine, Image } from "../protocol";
import { kindColour } from "../theme";
import { useController } from "./hooks";

interface ComposerProps {
  sessionId: string;
  roster: AgentLine[];
  busy: boolean;
  online: boolean;
}

export function Composer({ sessionId, roster, busy, online }: ComposerProps) {
  const controller = useController();
  const [text, setText] = useState("");
  const [images, setImages] = useState<Image[]>([]);
  const [sending, setSending] = useState(false);
  const [problem, setProblem] = useState<string | null>(null);
  const fileRef = useRef<HTMLInputElement>(null);
  const areaRef = useRef<HTMLTextAreaElement>(null);

  const attachedBytes = images.reduce((n, i) => n + (i.data_b64?.length ?? 0), 0);
  const current = activeAgent(text);

  const resize = () => {
    const el = areaRef.current;
    if (el === null) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 160)}px`;
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
          placeholder={busy ? "Queue a message" : "Message"}
          aria-label="Message"
          enterkeyhint="send"
          onInput={(e) => {
            setText(e.currentTarget.value);
            resize();
          }}
          onKeyDown={(e) => {
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
