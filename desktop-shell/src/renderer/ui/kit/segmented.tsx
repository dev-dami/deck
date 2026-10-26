import { cva } from "class-variance-authority";

// A segmented control (Settings, the side panel's views): equal segments
// in a clear glass capsule, the chosen one a raised pill of brighter glass.
export const segmentTrack = cva("liquid-clear inline-flex items-center gap-0.5 rounded-full p-[3px]");

export const segmentItem = cva(
  "flex h-6 shrink-0 cursor-pointer items-center justify-center gap-1.5 whitespace-nowrap rounded-full px-3 text-xs outline-none transition-[background-color,color,box-shadow] focus-visible:ring-2 focus-visible:ring-ring",
  {
    defaultVariants: { on: false },
    variants: {
      on: {
        true: "bg-segment font-medium text-foreground shadow-card inset-shadow-hl",
        false: "text-muted-foreground hover:text-foreground",
      },
    },
  },
);
