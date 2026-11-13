"use client"

import { CheckIcon, CopyIcon } from "lucide-react"
import { useState, useSyncExternalStore } from "react"

import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

const noop = () => () => {}

/** The server's MCP URL with a copy button; the origin is known only in the browser. */
export function McpEndpoint({ className }: { className?: string }) {
  const origin = useSyncExternalStore(noop, () => window.location.origin, () => null)
  const [copied, setCopied] = useState(false)
  const url = origin ? `${origin}/mcp` : "/mcp"

  return (
    <div
      className={cn(
        "inline-flex max-w-full items-center gap-1 rounded-xl bg-card py-1 pr-1 pl-3.5 text-left shadow-press ring-1 ring-border",
        className
      )}
    >
      <span className="eyebrow mr-1 text-brand">MCP</span>
      <code className="truncate font-mono text-[0.8rem] text-foreground">{url}</code>
      <Button
        variant="ghost"
        size="icon-sm"
        aria-label="Copy MCP URL"
        onClick={async () => {
          await navigator.clipboard.writeText(url)
          setCopied(true)
          setTimeout(() => setCopied(false), 1500)
        }}
      >
        {copied ? <CheckIcon className="text-success" /> : <CopyIcon />}
      </Button>
    </div>
  )
}
