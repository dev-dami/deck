import { cleanLabel, isVideo, MAX_LABEL, type Outgoing, type VideoGate } from "@web/images";
import { type KeyboardEvent, useState } from "react";
import { cn } from "../lib/cn";
import { FilmStrip, X } from "./icons";
import { Button } from "./kit/button";

// One attachment over the composer's prompt: its thumbnail, the label the
// agent sees it by (click to rename: Enter keeps it, Esc does not), and
// remove. A video the lead cannot watch shows why, and offers to go as
// frames instead.
export function AttachmentChip({
  att,
  url,
  gate,
  onRename,
  onRemove,
  onFrames,
}: {
  att: Outgoing;
  url: string;
  gate: VideoGate;
  onRename: (label: string) => void;
  onRemove: () => void;
  onFrames: (on: boolean) => void;
}) {
  const [editing, setEditing] = useState<string | null>(null);
  const label = att.label ?? att.name;
  const video = isVideo(att.mime);
  // A video the lead cannot watch, not yet set to go as frames.
  const blocked = video && !gate.ok && att.frames !== true;

  const save = () => {
    if (editing === null) return;
    const next = cleanLabel(editing);
    if (next !== "" && next !== label) onRename(next);
    setEditing(null);
  };
  const onKey = (e: KeyboardEvent<HTMLInputElement>) => {
    // The prompt and the window never see these keys.
    e.stopPropagation();
    if (e.key === "Enter") {
      e.preventDefault();
      save();
    } else if (e.key === "Escape") {
      e.preventDefault();
      setEditing(null);
    }
  };

  return (
    <div
      className={cn(
        "attachment relative flex max-w-full animate-enter items-center gap-2 rounded-[12px] bg-secondary/70 p-1 pr-1.5 ring-1 ring-hairline",
        blocked && "attachment-blocked",
      )}
      data-kind={video ? "video" : "image"}
      title={att.name}
    >
      <span className={cn("thumb relative grid size-10 shrink-0 place-items-center overflow-hidden rounded-[8px] bg-secondary", blocked && "opacity-45")}>
        {video ? (
          <>
            <video src={url} muted preload="metadata" className="size-full object-cover" aria-hidden />
            <span className="absolute inset-0 grid place-items-center bg-black/25 text-white">
              <FilmStrip className="size-4" weight="fill" />
            </span>
          </>
        ) : (
          <img src={url} alt={label} className="size-full object-cover" />
        )}
      </span>
      <div className="flex min-w-0 flex-col items-start leading-tight">
        {editing === null ? (
          <button
            type="button"
            className="attachment-label max-w-[11rem] cursor-text truncate rounded-[4px] text-left text-[12.5px] text-foreground outline-none hover:underline hover:decoration-faint hover:underline-offset-2 focus-visible:ring-2 focus-visible:ring-ring"
            aria-label={`Rename ${label}`}
            title="Rename: the agent sees this label next to it"
            onClick={() => setEditing(label)}
          >
            {label}
          </button>
        ) : (
          <input
            // biome-ignore lint/a11y/noAutofocus: the person just asked to rename it
            autoFocus
            aria-label="Attachment label"
            value={editing}
            maxLength={MAX_LABEL}
            onFocus={(e) => e.currentTarget.select()}
            onChange={(e) => setEditing(e.target.value)}
            onKeyDown={onKey}
            onBlur={save}
            className="attachment-label-input w-[11rem] rounded-[5px] bg-background px-1 text-[12.5px] text-foreground outline-none ring-1 ring-primary"
          />
        )}
        {video && !gate.ok && (
          <span className={cn("attachment-note mt-0.5 max-w-[15rem] truncate text-[11px]", blocked ? "text-warning" : "text-faint")} role={blocked ? "status" : undefined}>
            {att.frames === true ? "Goes as still frames" : gate.reason}
          </span>
        )}
      </div>
      {video && !gate.ok && (
        <Button
          variant={att.frames === true ? "ghost-muted" : "outline"}
          size="xs"
          className="ml-1"
          disabled={att.frames !== true && !gate.frames}
          title={att.frames === true ? "Send the video itself" : gate.frames ? "Send up to 8 still frames from the video, labelled with their time" : gate.framesReason}
          aria-pressed={att.frames === true}
          onClick={() => onFrames(att.frames !== true)}
        >
          {att.frames === true ? "As frames ✓" : "Send as frames"}
        </Button>
      )}
      <button
        type="button"
        className="thumb-x grid size-5 shrink-0 cursor-pointer place-items-center rounded-full text-muted-foreground outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        aria-label={`Remove ${att.name}`}
        onClick={onRemove}
      >
        <X className="size-3" weight="bold" />
      </button>
    </div>
  );
}
