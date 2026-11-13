"use client"

import { useSyncExternalStore } from "react"

import { CopyButton } from "@/components/ui/copy-button"
import { cn } from "@/lib/utils"

const noop = () => () => {}

/** The server's MCP URL with a copy button; the origin is known only in the browser. */
export function McpEndpoint({ className }: { className?: string }) {
  const origin = useSyncExternalStore(noop, () => window.location.origin, () => null)
  const url = origin ? `${origin}/mcp` : "/mcp"

  return (
    <div
      className={cn(
        "inline-flex max-w-full items-center gap-1 rounded-xl bg-card py-1 pr-1 pl-3.5 text-left shadow-press ring-1 ring-border",
        className
      )}
    >
      <span className="eyebrow mr-1 text-brand-strong">MCP</span>
      <code className="truncate font-mono text-[0.8rem] text-foreground">{url}</code>
      <CopyButton value={url} label="Copy MCP URL" iconOnly variant="ghost" />
    </div>
  )
}
