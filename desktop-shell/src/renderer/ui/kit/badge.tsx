import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";
import { cn } from "../../lib/cn";

// After T3 Code's components/ui/badge.tsx.
const badgeVariants = cva(
  "badge inline-flex h-4.5 min-w-4.5 shrink-0 items-center justify-center gap-1 whitespace-nowrap rounded-full px-1.5 font-medium text-2xs tabular-nums leading-none",
  {
    defaultVariants: { variant: "default" },
    variants: {
      variant: {
        default: "bg-primary text-primary-foreground",
        warning: "bg-warning/15 text-warning-foreground",
        muted: "bg-selected text-muted-foreground",
        outline: "border border-hairline-strong text-muted-foreground",
        success: "bg-success/12 text-success-foreground",
        error: "bg-destructive/12 text-destructive-foreground",
      },
    },
  },
);

export function Badge({
  className,
  variant,
  ...props
}: ComponentProps<"span"> & VariantProps<typeof badgeVariants>) {
  return <span data-slot="badge" className={cn(badgeVariants({ variant }), className)} {...props} />;
}

export function Kbd({ className, ...props }: ComponentProps<"kbd">) {
  return (
    <kbd
      className={cn(
        "pointer-events-none inline-flex h-5 min-w-5 select-none items-center justify-center gap-1 rounded border border-hairline px-1 font-mono text-3xs text-faint",
        className,
      )}
      {...props}
    />
  );
}
