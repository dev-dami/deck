import { cva } from "class-variance-authority";

// A segmented control (Settings, the side panel's views): equal segments
// in a clear glass capsule, the chosen one a raised pill of brighter glass.
// The pill is the track's own (seg-track, liquidGlass.ts): it slides and
// stretches from one choice to the next.
export const segmentTrack = cva("seg-track liquid-clear relative inline-flex items-center gap-0.5 rounded-full p-[3px]");

export const segmentItem = cva(
  "seg-item flex h-6 shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-full px-3 text-xs outline-none transition-[background-color,color,box-shadow] focus-visible:ring-2 focus-visible:ring-ring",
  {
    defaultVariants: { on: false },
    variants: {
      on: {
        true: "seg-on bg-segment font-medium text-foreground shadow-card inset-shadow-hl",
        false: "text-muted-foreground hover:text-foreground",
      },
    },
  },
);
