import { cn } from "../../lib/cn";

// An on/off switch with its name for screen readers, drawn as Tahoe's: a
// recessed capsule that fills with the accent, and a knob of white glass,
// a little wider than tall, that swells while it is pressed. Only the
// accent's opacity and the knob's transform move.
export function Switch({ checked, onChange, label }: { checked: boolean; onChange: (v: boolean) => void; label: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      aria-label={label}
      onClick={() => onChange(!checked)}
      className="group relative inline-flex h-[22px] w-[38px] shrink-0 cursor-pointer rounded-full bg-[color-mix(in_srgb,var(--text)_15%,transparent)] shadow-[inset_0_1px_2px_rgb(0_0_0/0.14),inset_0_0_0_0.5px_rgb(0_0_0/0.06)] outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background"
    >
      <span
        aria-hidden
        className={cn(
          "absolute inset-0 rounded-full bg-primary bg-[linear-gradient(180deg,rgb(255_255_255/0.18),rgb(255_255_255/0)_60%)] shadow-[inset_0_1px_0_rgb(255_255_255/0.22)] transition-opacity duration-200",
          checked ? "opacity-100" : "opacity-0",
        )}
      />
      <span
        aria-hidden
        className={cn(
          "absolute top-[2px] left-[2px] h-[18px] w-[22px] rounded-full bg-white bg-[linear-gradient(180deg,#fff,rgb(240_240_244))] shadow-[0_1px_2px_rgb(0_0_0/0.22),0_2px_6px_-1px_rgb(0_0_0/0.18),inset_0_0_0_0.5px_rgb(0_0_0/0.06)] transition-transform duration-200 ease-(--ease) group-active:scale-x-[1.14] group-active:scale-y-[1.06]",
          checked ? "translate-x-[12px] group-active:origin-right" : "translate-x-0 group-active:origin-left",
        )}
      />
    </button>
  );
}
