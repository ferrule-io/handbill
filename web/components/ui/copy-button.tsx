"use client"

import { CheckIcon, CopyIcon } from "lucide-react"
import { useEffect, useRef, useState } from "react"
import { toast } from "sonner"

import { Button } from "@/components/ui/button"

type ButtonProps = React.ComponentProps<typeof Button>

/**
 * The one copy-to-clipboard affordance: copies `value`, swaps the icon (and
 * label) to a check for a moment, and announces "Copied" to screen readers.
 * With `iconOnly`, `label` becomes the accessible name. When the clipboard
 * is unavailable, it says so and calls `onFailed` (e.g. to select the text).
 */
export function CopyButton({
  value,
  label = "Copy",
  copiedLabel = "Copied",
  iconOnly = false,
  onFailed,
  variant = "outline",
  size,
  ...props
}: Omit<ButtonProps, "onClick" | "children" | "value"> & {
  value: string
  label?: string
  copiedLabel?: string
  iconOnly?: boolean
  onFailed?: () => void
}) {
  const [copied, setCopied] = useState(false)
  const timer = useRef<number>(undefined)
  useEffect(() => () => window.clearTimeout(timer.current), [])

  async function copy() {
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      toast.error("Couldn’t copy. Select the text and copy it yourself.")
      onFailed?.()
      return
    }
    setCopied(true)
    window.clearTimeout(timer.current)
    timer.current = window.setTimeout(() => setCopied(false), 1800)
  }

  const icon = copied ? <CheckIcon className={variant === "brand" ? undefined : "text-success"} /> : <CopyIcon />
  return (
    <>
      <Button
        type="button"
        variant={variant}
        size={size ?? (iconOnly ? "icon-sm" : "default")}
        onClick={copy}
        aria-label={iconOnly ? label : undefined}
        {...props}
      >
        {icon}
        {!iconOnly && (copied ? copiedLabel : label)}
      </Button>
      <span className="sr-only" role="status" aria-live="polite">
        {copied ? copiedLabel : ""}
      </span>
    </>
  )
}
