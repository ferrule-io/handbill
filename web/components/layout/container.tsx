import { cn } from "@/lib/utils"

const widths = {
  narrow: "max-w-2xl",
  default: "max-w-5xl",
  wide: "max-w-7xl",
} as const

/** Horizontally centered column with the standard page gutters. */
export function Container({
  size = "default",
  className,
  ...props
}: React.ComponentProps<"div"> & { size?: keyof typeof widths }) {
  return (
    <div
      className={cn("mx-auto w-full px-5 sm:px-8", widths[size], className)}
      {...props}
    />
  )
}

/** Vertical rhythm: children separated by a consistent gap. */
export function Stack({
  gap = "default",
  className,
  ...props
}: React.ComponentProps<"div"> & { gap?: "tight" | "default" | "loose" }) {
  return (
    <div
      className={cn(
        "flex flex-col",
        gap === "tight" && "gap-3",
        gap === "default" && "gap-6",
        gap === "loose" && "gap-12",
        className
      )}
      {...props}
    />
  )
}
