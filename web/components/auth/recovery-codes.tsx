"use client"

import { CheckIcon, CopyIcon, DownloadIcon } from "lucide-react"
import { useState } from "react"

import { Button } from "@/components/ui/button"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

/**
 * A freshly issued set of recovery codes, printed like a proof sheet:
 * numbered, monospaced, with copy and download. Shown once.
 */
export function RecoveryCodes({ codes, email, className }: { codes: string[] | null; email?: string; className?: string }) {
  const [copied, setCopied] = useState(false)
  const text = codes ? recoveryFile(codes, email) : ""

  async function copy() {
    if (!codes) return
    await navigator.clipboard.writeText(codes.join("\n"))
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  function download() {
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }))
    const a = document.createElement("a")
    a.href = url
    a.download = `agent-dash-recovery-codes-${location.hostname}.txt`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className={cn("@container overflow-hidden rounded-2xl bg-card shadow-press ring-1 ring-border", className)}>
      <div className="flex items-center justify-between border-b border-dashed border-border px-4 py-2.5">
        <span className="eyebrow">Recovery codes</span>
        <span className="font-mono text-[0.68rem] text-muted-foreground">single use · keep offline</span>
      </div>
      <ol className="grid grid-cols-1 gap-x-8 gap-y-2 px-4 py-4 @[27rem]:grid-cols-2 @[27rem]:px-5" aria-label="Recovery codes">
        {(codes ?? Array.from({ length: 10 }, () => null)).map((code, i) => (
          <li key={i} className="flex items-baseline gap-2.5 font-mono text-[0.8rem] tabular-nums">
            <span className="w-5 text-right text-[0.65rem] text-muted-foreground">{String(i + 1).padStart(2, "0")}</span>
            {code ? (
              <span data-testid="recovery-code" className="tracking-wide whitespace-nowrap text-foreground select-all">
                {code}
              </span>
            ) : (
              <Skeleton className="h-4 w-36" />
            )}
          </li>
        ))}
      </ol>
      <div className="flex gap-2 border-t border-border bg-muted/50 px-3 py-2.5">
        <Button type="button" variant="outline" size="sm" onClick={copy} disabled={!codes}>
          {copied ? <CheckIcon /> : <CopyIcon />}
          {copied ? "Copied" : "Copy"}
        </Button>
        <Button type="button" variant="outline" size="sm" onClick={download} disabled={!codes}>
          <DownloadIcon />
          Download .txt
        </Button>
      </div>
    </div>
  )
}

function recoveryFile(codes: string[], email?: string) {
  const lines = ["agent-dash recovery codes"]
  if (typeof location !== "undefined") lines.push(`Server:  ${location.origin}`)
  if (email) lines.push(`Account: ${email}`)
  lines.push(
    `Issued:  ${new Date().toISOString().slice(0, 10)}`,
    "",
    "Each code works once. Use one at /login/recover if you lose every passkey.",
    "Generating new codes invalidates these.",
    "",
    ...codes.map((c, i) => `${String(i + 1).padStart(2, "0")}  ${c}`),
    ""
  )
  return lines.join("\n")
}
