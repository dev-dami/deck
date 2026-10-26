import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";
import { cn } from "../../lib/cn";

// After T3 Code's components/ui/button.tsx, without Base UI: a plain button,
// drawn as a Liquid Glass capsule (docs/design-glass.md). The default is the
// accent as tinted glass; outline is clear glass; ghost has no material
// until it is hovered or pressed.
export const buttonVariants = cva(
  "relative inline-flex shrink-0 cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-full border font-medium text-sm outline-none transition-[background-color,border-color,color,box-shadow,scale] active:scale-[0.97] focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    defaultVariants: { size: "default", variant: "default" },
    variants: {
      size: {
        default: "h-8 px-3",
        sm: "h-7 gap-1.5 px-2.5",
        xs: "h-6 gap-1 px-2.5 text-xs [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-10 px-4",
        icon: "size-8",
        "icon-sm": "size-7",
        "icon-xs": "size-6 [&_svg:not([class*='size-'])]:size-3.5",
      },
      variant: {
        default: "liquid-tint hover:brightness-110",
        destructive: "liquid-tint bg-destructive text-white hover:brightness-110",
        "destructive-outline":
          "liquid-clear text-destructive-foreground hover:bg-destructive/12",
        outline:
          "liquid-clear text-foreground hover:bg-selected aria-pressed:bg-selected",
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
