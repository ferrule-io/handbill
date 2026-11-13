"use client"

import { CheckIcon, CopyIcon, PencilIcon } from "lucide-react"
import { useState } from "react"
import { toast } from "sonner"

import {
  absoluteTime,
  can,
  formatBytes,
  plural,
  relativeTime,
  roleLabel,
  type Site,
  type Version,
} from "@/components/site/data"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Textarea } from "@/components/ui/textarea"
import { api, ApiError } from "@/lib/api"

export function SiteOverview({
  site,
  currentVersion,
  onChange,
}: {
  site: Site
  currentVersion: Version | null
  onChange: () => void
}) {
  const [editing, setEditing] = useState(false)
  return (
    <div className="overflow-hidden rounded-2xl bg-card shadow-press ring-1 ring-border">
        {editing ? (
          <EditDetails
            site={site}
            onDone={(saved) => {
              setEditing(false)
              if (saved) onChange()
            }}
          />
        ) : (
          <div className="flex items-start justify-between gap-4 p-5">
            <div className="flex min-w-0 flex-col gap-1">
              <p className="eyebrow">Details</p>
              <p className="font-heading text-lg font-medium">{site.title}</p>
              <p className="text-sm leading-relaxed whitespace-pre-line text-muted-foreground">
                {site.description || "No description yet."}
              </p>
            </div>
            {can.edit(site.role) && (
              <Button variant="outline" size="sm" onClick={() => setEditing(true)} data-testid="edit-details">
                <PencilIcon /> Edit
              </Button>
            )}
          </div>
        )}
        <dl className="flex flex-col divide-y divide-border border-t border-border text-sm">
          <Fact label="Address">
            <SiteAddress url={site.url} />
          </Fact>
          <Fact label="Live version">
            {currentVersion ? (
              <span data-testid="current-version">
                Published <time title={absoluteTime(currentVersion.createdAt)}>{relativeTime(currentVersion.createdAt)}</time>
                {currentVersion.publishedBy && <> by {currentVersion.publishedBy}</>}
                <span className="text-muted-foreground">
                  {" · "}
                  {plural(currentVersion.fileCount, "file")} · {formatBytes(currentVersion.size)}
                </span>
              </span>
            ) : (
              <span className="text-muted-foreground">
                Nothing published yet. Ask an agent to publish to this site over MCP.
              </span>
            )}
          </Fact>
          <div className="grid divide-y divide-border sm:grid-cols-3 sm:divide-x sm:divide-y-0">
            <Fact label="Your role">{roleLabel[site.role]}</Fact>
            <Fact label="Created">
              <time title={absoluteTime(site.createdAt)}>{absoluteTime(site.createdAt)}</time>
            </Fact>
            <Fact label="Last updated">
              <time title={absoluteTime(site.updatedAt)}>{relativeTime(site.updatedAt)}</time>
            </Fact>
          </div>
        </dl>
    </div>
  )
}

function Fact({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1 px-5 py-3.5">
      <dt className="eyebrow">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  )
}

function SiteAddress({ url }: { url: string }) {
  const [copied, setCopied] = useState(false)
  return (
    <div className="flex min-w-0 items-center gap-1">
      <a
        href={url}
        target="_blank"
        rel="noreferrer"
        className="truncate font-mono text-[0.85rem] underline-offset-4 hover:underline"
      >
        {url}
      </a>
      <Button
        variant="ghost"
        size="icon-xs"
        aria-label="Copy site address"
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

function EditDetails({ site, onDone }: { site: Site; onDone: (saved: boolean) => void }) {
  const [title, setTitle] = useState(site.title)
  const [description, setDescription] = useState(site.description)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  async function save(e: React.FormEvent) {
    e.preventDefault()
    setSaving(true)
    setError(null)
    try {
      await api(`/app-api/sites/${site.id}`, { method: "PATCH", body: { title, description } })
      toast.success("Details saved")
      onDone(true)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Could not save.")
      setSaving(false)
    }
  }

  return (
    <form onSubmit={save} className="flex flex-col gap-4 p-5">
      <div className="flex flex-col gap-2">
        <Label htmlFor="site-title">Title</Label>
        <Input id="site-title" value={title} onChange={(e) => setTitle(e.target.value)} maxLength={200} required autoFocus />
      </div>
      <div className="flex flex-col gap-2">
        <Label htmlFor="site-description">Description</Label>
        <Textarea
          id="site-description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          maxLength={2000}
          rows={3}
          placeholder="What is this site for?"
        />
      </div>
      {error && (
        <p role="alert" className="text-sm text-destructive">
          {error}
        </p>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" onClick={() => onDone(false)} disabled={saving}>
          Cancel
        </Button>
        <Button type="submit" disabled={saving || title.trim() === ""}>
          {saving ? "Saving…" : "Save"}
        </Button>
      </div>
    </form>
  )
}
