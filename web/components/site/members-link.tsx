"use client"

import { EyeOffIcon, XIcon } from "lucide-react"
import { useEffect, useRef } from "react"

import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/ui/copy-button"
import { cn } from "@/lib/utils"

/**
 * A freshly issued link (invite, public link, ownership transfer), shown
 * once: the server keeps only its hash, so closing this panel loses it.
 */
export function IssuedLink({
  title,
  link,
  children,
  onDismiss,
  testId,
  className,
}: {
  title: React.ReactNode
  link: string
  children?: React.ReactNode
  onDismiss: () => void
  testId?: string
  className?: string
}) {
  const field = useRef<HTMLInputElement>(null)
  useEffect(() => {
    field.current?.focus()
    field.current?.select()
  }, [link])

  return (
    <div
      role="status"
      data-testid={testId}
      className={cn(
        "relative flex flex-col gap-4 overflow-hidden rounded-2xl bg-brand-soft/50 p-5 ring-1 ring-brand/30 animate-in fade-in slide-in-from-top-1",
        className
      )}
    >
      <div className="flex items-start gap-3">
        <div className="flex min-w-0 flex-1 flex-col gap-1">
          <p className="font-medium break-words">{title}</p>
          {children && <div className="text-sm leading-relaxed text-muted-foreground">{children}</div>}
        </div>
        <Button variant="ghost" size="icon-sm" aria-label="Dismiss" onClick={onDismiss} className="-mt-1 -mr-2">
          <XIcon />
        </Button>
      </div>
      <div className="flex flex-col gap-2 sm:flex-row">
        <input
          ref={field}
          readOnly
          value={link}
          aria-label="Link"
          data-testid="issued-link"
          onFocus={(e) => e.currentTarget.select()}
          className="h-10 min-w-0 rounded-lg bg-card px-3 font-mono text-[0.8rem] text-foreground ring-1 ring-input outline-none focus-visible:ring-2 focus-visible:ring-ring pointer-coarse:h-11 sm:flex-1"
        />
        <CopyButton
          value={link}
          label="Copy link"
          variant="brand"
          size="lg"
          className="h-10 sm:w-32"
          onFailed={() => field.current?.select()}
          data-testid="copy-link"
        />
      </div>
      <p className="flex items-start gap-1.5 text-xs text-muted-foreground">
        <EyeOffIcon className="mt-px size-3.5 shrink-0" aria-hidden="true" />
        Shown only now. We keep a fingerprint of it, not the link, so it can’t be displayed again.
      </p>
    </div>
  )
}
