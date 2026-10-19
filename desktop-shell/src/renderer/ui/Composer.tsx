import { canSend } from "@web/composer";
import { checkImage, displayName, readAsBase64 } from "@web/images";
import type { AgentLine, Image } from "@web/protocol";
import { kindColour } from "@web/theme";
import { type ClipboardEvent, type KeyboardEvent, useMemo, useRef, useState } from "react";
import { applySuggestion, type Mention, mentionAt, suggestions } from "../completion";

export function Composer({
  draft,
  setDraft,
  roster,
  kinds,
  images: imagesOn,
  online,
  onSend,
  onError,
  inputRef,
}: {
  draft: string;
  setDraft: (s: string) => void;
  roster: AgentLine[];
  kinds: string[];
  images: boolean;
  online: boolean;
  onSend: (text: string, images: Image[]) => Promise<boolean>;
  onError: (text: string) => void;
  inputRef: React.RefObject<HTMLTextAreaElement | null>;
}) {
  const [images, setImages] = useState<Image[]>([]);
  const [mention, setMention] = useState<Mention | null>(null);
  const [pick, setPick] = useState(0);
  const [sending, setSending] = useState(false);
  const composing = useRef(false);

  const options = useMemo(
    () => (mention === null ? [] : suggestions(mention.query, roster, kinds).slice(0, 8)),
    [mention, roster, kinds],
  );

  const track = (el: HTMLTextAreaElement) => {
    setMention(mentionAt(el.value, el.selectionStart));
    setPick(0);
  };

  const accept = (i: number) => {
    const s = options[i];
    const el = inputRef.current;
    if (s === undefined || mention === null || el === null) return;
    const next = applySuggestion(draft, mention, s);
    setDraft(next.text);
    setMention(null);
    requestAnimationFrame(() => {
      el.focus();
      el.setSelectionRange(next.caret, next.caret);
    });
  };

  const send = async () => {
    if (sending || !canSend(draft, images.length)) return;
    setSending(true);
    const ok = await onSend(draft, images);
    setSending(false);
    if (ok) {
      setDraft("");
      setImages([]);
      setMention(null);
    }
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (composing.current || e.nativeEvent.isComposing) return;
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
        setMention(null);
        return;
      }
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void send();
    }
  };

  const onPaste = async (e: ClipboardEvent<HTMLTextAreaElement>) => {
    if (!imagesOn) return;
    const files = [...e.clipboardData.files].filter((f) => f.type.startsWith("image/"));
    if (files.length === 0) return;
    e.preventDefault();
    let used = images.reduce((n, im) => n + (im.data_b64?.length ?? 0), 0);
    const added: Image[] = [];
    for (const f of files) {
      const check = checkImage(f, used);
      if (!check.ok) {
        onError(check.reason);
        continue;
      }
      const data = await readAsBase64(f);
      used += data.length;
      added.push({ name: displayName(f.name || "pasted.png"), mime: f.type, data_b64: data });
    }
    setImages((cur) => [...cur, ...added]);
  };

  return (
    <div className="composer">
      {options.length > 0 && (
        <ul className="suggest" role="listbox" aria-label="Agents">
          {options.map((s, i) => (
            <li
              key={s.insert}
              role="option"
              aria-selected={i === pick}
              className={i === pick ? "on" : ""}
              onMouseDown={(e) => {
                e.preventDefault();
                accept(i);
              }}
            >
              <span className="suggest-insert" style={{ color: s.kind === null ? undefined : kindColour(s.kind) }}>
                {s.insert}
              </span>
              <span className="suggest-detail">{s.detail}</span>
            </li>
          ))}
        </ul>
      )}
      {images.length > 0 && (
        <div className="attachments">
          {images.map((im, i) => (
            <span key={i} className="chip">
              {im.name}
              <button
                type="button"
                className="chip-x"
                aria-label={`Remove ${im.name}`}
                onClick={() => setImages((cur) => cur.filter((_, j) => j !== i))}
              >
                ×
              </button>
            </span>
          ))}
        </div>
      )}
      <div className="composer-box">
        <textarea
          ref={inputRef}
          aria-label="Message"
          placeholder={online ? "Message the agents. Type $ to pick one, $codex+ to start another." : "Offline. Reconnecting…"}
          value={draft}
          rows={Math.min(10, Math.max(2, draft.split("\n").length))}
          onChange={(e) => {
            setDraft(e.target.value);
            track(e.target);
          }}
          onSelect={(e) => track(e.currentTarget)}
          onKeyDown={onKeyDown}
          onPaste={(e) => void onPaste(e)}
          onCompositionStart={() => {
            composing.current = true;
          }}
          onCompositionEnd={() => {
            composing.current = false;
          }}
          onBlur={() => setMention(null)}
        />
        <button
          type="button"
          className="send"
          onClick={() => void send()}
          disabled={!online || sending || !canSend(draft, images.length)}
        >
          Send
        </button>
      </div>
      <p className="hint">Enter sends. Shift+Enter adds a line.</p>
    </div>
  );
}
