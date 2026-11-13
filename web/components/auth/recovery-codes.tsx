"use client"

import { DownloadIcon } from "lucide-react"

import { Button } from "@/components/ui/button"
import { CopyButton } from "@/components/ui/copy-button"
import { Skeleton } from "@/components/ui/skeleton"
import { cn } from "@/lib/utils"

/**
 * A freshly issued set of recovery codes, printed like a proof sheet:
 * numbered, monospaced, with copy and download. Shown once.
 */
export function RecoveryCodes({ codes, email, className }: { codes: string[] | null; email?: string; className?: string }) {
  const text = codes ? recoveryFile(codes, email) : ""

  function download() {
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain;charset=utf-8" }))
    const a = document.createElement("a")
    a.href = url
    a.download = `handbill-recovery-codes-${location.hostname}.txt`
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className={cn("@container overflow-hidden rounded-2xl bg-card shadow-press ring-1 ring-border", className)}>
      <div className="flex items-center justify-between gap-3 border-b border-dashed border-border px-4 py-2.5">
        <span className="eyebrow">Recovery codes</span>
        <span className="font-mono text-[0.68rem] text-muted-foreground">single use · keep offline</span>
      </div>
      <ol
        className="grid grid-cols-1 gap-x-8 gap-y-2 px-4 py-4 @[27rem]:grid-cols-2 @[27rem]:px-5"
        aria-label="Recovery codes"
        aria-busy={!codes}
      >
        {(codes ?? Array.from({ length: 10 }, () => null)).map((code, i) => (
          <li key={i} className="flex items-baseline gap-2.5 font-mono text-[0.8rem] tabular-nums">
            <span className="w-5 text-right text-[0.65rem] text-muted-foreground">{String(i + 1).padStart(2, "0")}</span>
            {code ? (
              <span data-testid="recovery-code" className="tracking-wide whitespace-nowrap text-foreground select-all">
                {code}
              </span>
            ) : (
              <Skeleton className="h-4 w-36 max-w-full" />
            )}
          </li>
        ))}
      </ol>
      <div className="flex flex-wrap gap-2 border-t border-border bg-muted/50 px-3 py-2.5">
        <CopyButton
          value={codes?.join("\n") ?? ""}
          label="Copy codes"
          size="sm"
          disabled={!codes}
        />
        <Button type="button" variant="outline" size="sm" onClick={download} disabled={!codes}>
          <DownloadIcon />
          Download .txt
        </Button>
      </div>
    </div>
  )
}

function recoveryFile(codes: string[], email?: string) {
  const lines = ["Handbill recovery codes"]
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
