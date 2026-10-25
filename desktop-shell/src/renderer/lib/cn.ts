import { type ClassValue, clsx } from "clsx";
import { extendTailwindMerge } from "tailwind-merge";

// The theme's extra font sizes and shadows (styles.css); unregistered,
// tailwind-merge reads text-2xs as a colour and drops it next to
// text-muted-foreground.
const twMerge = extendTailwindMerge({
  extend: { theme: { text: ["2xs", "3xs", "code", "prose"], shadow: ["popover"], "inset-shadow": ["hl"] } },
});

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}
