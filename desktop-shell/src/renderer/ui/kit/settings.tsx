import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

// After T3 Code's settings layout, drawn as a Mac's System Settings: a
// section title, then a rounded group of rows on the sheet, each with a
// title, a description and a control on the right. The rows part with
// hairlines inset from the leading edge. A group is a size container, so a
// row stacks its control under its text when the group is narrow.
export function Section({ title, children, className }: { title: string; children: ReactNode; className?: string }) {
  return (
    <section className="flex flex-col gap-2">
      <h2 className="px-4 font-semibold text-muted-foreground text-xs">{title}</h2>
      <div
        className={cn(
          "@container rounded-(--sheet-radius) bg-surface-1 shadow-[0_1px_2px_rgb(0_0_0/0.04)] ring-1 ring-hairline",
          "[&>*+*]:relative [&>*+*]:before:pointer-events-none [&>*+*]:before:absolute [&>*+*]:before:inset-x-4 [&>*+*]:before:top-0 [&>*+*]:before:h-px [&>*+*]:before:bg-hairline",
          className,
        )}
      >
        {children}
      </div>
    </section>
  );
}

export function Row({ title, description, control }: { title: ReactNode; description?: ReactNode; control?: ReactNode }) {
  return (
    <div className="flex min-h-12 flex-col gap-2.5 px-4 py-3 @md:flex-row @md:items-center @md:justify-between @md:gap-6" data-slot="settings-row">
      <div className="min-w-0 space-y-0.5">
        <h3 className="font-medium text-[13px]">{title}</h3>
        {description !== undefined && <p className="text-pretty text-muted-foreground text-xs leading-relaxed">{description}</p>}
      </div>
      {control !== undefined && <div className="flex shrink-0 items-center gap-2">{control}</div>}
    </div>
  );
}
