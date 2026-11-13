"use client"

import { useCallback, useEffect, useState } from "react"

import { api, ApiError } from "@/lib/api"

/** Shapes of /app-api/sites… (internal/sites/api.go). */
export type Role = "owner" | "manager" | "viewer"

export type Site = {
  id: string
  title: string
  description: string
  url: string
  role: Role
  currentVersionId: string | null
  createdAt: string
  updatedAt: string
}

export type Version = {
  id: string
  publishedBy: string | null
  createdAt: string
  fileCount: number
  size: number
  current: boolean
}

/** Mirrors internal/rbac for what the UI offers; the server enforces it. */
export const can = {
  edit: (role: Role) => role === "owner" || role === "manager",
  publish: (role: Role) => role === "owner" || role === "manager",
  manageViewers: (role: Role) => role === "owner" || role === "manager",
  manageManagers: (role: Role) => role === "owner",
  managePublicLink: (role: Role) => role === "owner",
  transfer: (role: Role) => role === "owner",
}

export type Load<T> =
  | { state: "loading" }
  | { state: "error"; error: ApiError }
  | { state: "ready"; data: T }

/** Fetches path (skipped while null) and refetches on reload(). */
export function useApi<T>(path: string | null): [Load<T>, () => void] {
  const [load, setLoad] = useState<Load<T>>({ state: "loading" })
  const [tick, setTick] = useState(0)
  useEffect(() => {
    if (path === null) return
    const ctl = new AbortController()
    api<T>(path, { signal: ctl.signal })
      .then((data) => setLoad({ state: "ready", data }))
      .catch((err: unknown) => {
        if (ctl.signal.aborted) return
        const error = err instanceof ApiError ? err : new ApiError(0, "network", "Could not reach the server.")
        setLoad({ state: "error", error })
      })
    return () => ctl.abort()
  }, [path, tick])
  const reload = useCallback(() => setTick((t) => t + 1), [])
  return [load, reload]
}

const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: "auto" })
const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ["year", 365 * 24 * 3600],
  ["month", 30 * 24 * 3600],
  ["week", 7 * 24 * 3600],
  ["day", 24 * 3600],
  ["hour", 3600],
  ["minute", 60],
]

/** "3 hours ago", "yesterday", "just now". */
export function relativeTime(iso: string, now = Date.now()): string {
  const secs = (new Date(iso).getTime() - now) / 1000
  for (const [unit, size] of units) {
    if (Math.abs(secs) >= size) return rtf.format(Math.round(secs / size), unit)
  }
  return "just now"
}

const dateFmt = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" })

export function absoluteTime(iso: string): string {
  return dateFmt.format(new Date(iso))
}

export function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  const units = ["KB", "MB", "GB", "TB"]
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v)} ${units[i]}`
}

export function plural(n: number, one: string, many = `${one}s`): string {
  return `${n.toLocaleString()} ${n === 1 ? one : many}`
}

export const roleLabel: Record<Role, string> = { owner: "Owner", manager: "Manager", viewer: "Viewer" }
