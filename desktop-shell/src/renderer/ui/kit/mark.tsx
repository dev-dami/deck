import { useId } from "react";
import { cn } from "../../lib/cn";

// Deck's mark, as the app icon draws it (build/icon.png): an amber prompt
// chevron and a cursor bar. The glyph alone sits in type; its cursor takes
// the text colour, so it reads on light and dark. tile draws the whole app
// icon, its dark rounded square included.
export function DeckMark({ tile = false, className }: { tile?: boolean; className?: string }) {
  const id = useId();
  const amber = `${id}-amber`;
  const glyph = (
    <>
      <polyline
        points="310,360 505,512 310,662"
        fill="none"
        stroke={`url(#${amber})`}
        strokeWidth="84"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <rect x="563" y="590" width="191" height="72" rx="36" fill={tile ? "#f4eee4" : "currentColor"} />
    </>
  );
  return (
    <svg
      aria-hidden
      viewBox={tile ? "48 48 928 928" : "262 312 500 400"}
      className={cn("deck-mark shrink-0", tile ? "size-10" : "h-3.5 w-auto", className)}
    >
      <defs>
        <linearGradient id={amber} x1="0" y1="360" x2="0" y2="662" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#f7bb5c" />
          <stop offset="1" stopColor="#ea972f" />
        </linearGradient>
        {tile && (
          <linearGradient id={`${id}-tile`} x1="0" y1="48" x2="0" y2="976" gradientUnits="userSpaceOnUse">
            <stop offset="0" stopColor="#2b2822" />
            <stop offset="1" stopColor="#151411" />
          </linearGradient>
        )}
      </defs>
      {tile && <rect x="48" y="48" width="928" height="928" rx="206" fill={`url(#${id}-tile)`} />}
      {glyph}
    </svg>
  );
}
