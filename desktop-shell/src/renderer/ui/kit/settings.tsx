import type { ReactNode } from "react";
import { cn } from "../../lib/cn";

// After T3 Code's settings layout, without boxes: a section title in the
// foreground, then its rows, parted by hairlines, each with a title, a
// description and a control on the right. The rows are a size container,
// so a row stacks its control under its text when the column is narrow.
export function Section({ title, children, className }: { title: string; children: ReactNode; className?: string }) {
  return (
    <section className="flex flex-col">
      <h2 className="pb-1.5 font-semibold text-[13px]">{title}</h2>
      <div className={cn("@container border-hairline border-t [&>*+*]:border-hairline [&>*+*]:border-t", className)}>{children}</div>
    </section>
  );
}

export function Row({ title, description, control }: { title: ReactNode; description?: ReactNode; control?: ReactNode }) {
  return (
    <div className="flex min-h-12 flex-col gap-2.5 py-3 @md:flex-row @md:items-center @md:justify-between @md:gap-8" data-slot="settings-row">
      <div className="min-w-0 space-y-0.5">
        <h3 className="text-[13px]">{title}</h3>
        {description !== undefined && <p className="text-pretty text-muted-foreground text-xs leading-relaxed">{description}</p>}
      </div>
      {control !== undefined && <div className="flex shrink-0 items-center gap-2">{control}</div>}
    </div>
  );
}
