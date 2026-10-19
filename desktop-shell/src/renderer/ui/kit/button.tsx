import { cva, type VariantProps } from "class-variance-authority";
import type { ComponentProps } from "react";
import { cn } from "../../lib/cn";

// After T3 Code's components/ui/button.tsx, without Base UI: a plain button.
export const buttonVariants = cva(
  "relative inline-flex shrink-0 cursor-pointer items-center justify-center gap-2 whitespace-nowrap rounded-[var(--control-radius)] border font-medium text-sm outline-none transition-[background-color,color,box-shadow,scale] active:scale-[0.97] focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-1 focus-visible:ring-offset-background disabled:pointer-events-none disabled:opacity-50 [&_svg]:pointer-events-none [&_svg]:shrink-0 [&_svg:not([class*='size-'])]:size-4",
  {
    defaultVariants: { size: "default", variant: "default" },
    variants: {
      size: {
        default: "h-8 px-3",
        sm: "h-7 gap-1.5 px-2.5 text-[13px]",
        xs: "h-6 gap-1 rounded-md px-2 text-xs [&_svg:not([class*='size-'])]:size-3.5",
        lg: "h-10 px-4",
        icon: "size-8",
        "icon-sm": "size-7",
        "icon-xs": "size-6 rounded-md [&_svg:not([class*='size-'])]:size-3.5",
      },
      variant: {
        default: "border-primary bg-primary text-primary-foreground shadow-xs hover:bg-primary/90",
        destructive: "border-destructive bg-destructive text-white shadow-xs hover:bg-destructive/90",
        "destructive-outline":
          "border-input bg-popover text-destructive-foreground hover:border-destructive/40 hover:bg-destructive/8",
        outline: "border-input bg-popover text-foreground shadow-xs/5 hover:bg-accent",
        secondary: "border-transparent bg-secondary text-foreground hover:bg-accent",
        ghost: "border-transparent text-foreground hover:bg-accent aria-pressed:bg-accent",
        "ghost-muted":
          "border-transparent text-muted-foreground hover:bg-accent hover:text-foreground aria-pressed:bg-accent aria-pressed:text-foreground",
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
