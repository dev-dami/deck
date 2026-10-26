import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";
import { cn } from "../../lib/cn";

// After T3 Code's components/ui/button.tsx, without Base UI: a plain button.
// Raised buttons carry a 1px top highlight instead of a shadow. Hover and
// pressed are a white alpha over the surface, never the accent.
export const buttonVariants = cva(
  "relative inline-flex shrink-0 cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-[var(--control-radius)] border font-medium text-sm outline-none transition-[background-color,border-color,color,box-shadow,scale] active:scale-[0.97] focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    defaultVariants: { size: "default", variant: "default" },
    variants: {
      size: {
        default: "h-8 px-3",
        sm: "h-7 gap-1.5 px-2.5",
        xs: "h-6 gap-1 rounded-md px-2 text-xs [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-10 px-4",
        icon: "size-8",
        "icon-sm": "size-7",
        "icon-xs": "size-6 rounded-md [&_svg:not([class*='size-'])]:size-3.5",
      },
      variant: {
        default: "border-action bg-action text-action-foreground inset-shadow-hl hover:bg-action/90",
        destructive: "border-destructive bg-destructive text-white inset-shadow-hl hover:bg-destructive/90",
        "destructive-outline":
          "border-hairline-strong bg-surface-1 text-destructive-foreground inset-shadow-hl hover:border-destructive/40 hover:bg-destructive/8",
        outline:
          "border-hairline-strong bg-surface-1 text-foreground shadow-card inset-shadow-hl hover:bg-surface-2 aria-pressed:bg-surface-2 aria-pressed:border-hairline-strong",
        secondary: "border-transparent bg-secondary text-foreground hover:bg-selected",
        ghost: "border-transparent text-foreground hover:bg-hover aria-pressed:bg-selected",
        "ghost-muted":
          "border-transparent text-muted-foreground hover:bg-hover hover:text-foreground aria-pressed:bg-selected aria-pressed:text-foreground",
        link: "border-transparent text-muted-foreground underline-offset-4 hover:text-foreground hover:underline",
      },
    },
  },
);

export type ButtonProps = ComponentProps<"button"> & VariantProps<typeof buttonVariants>;

export function Button({ className, variant, size, type, ...props }: ButtonProps) {
  return (
    <button
      type={type ?? "button"}
      data-slot="button"
      className={cn(buttonVariants({ variant, size }), className)}
      {...props}
    />
  );
}
