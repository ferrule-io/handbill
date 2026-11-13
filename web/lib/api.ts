"use client"

import { useEffect, useSyncExternalStore } from "react"

/**
 * Client for the app's JSON API under /app-api/ (docs/design.md, "CSRF and
 * CORS"). Requests carry the session cookie; unsafe methods also send the
 * CSRF token from GET /app-api/me. Failures throw ApiError with the
 * server's {"error": {code, message}}.
 */

export class ApiError extends Error {
  status: number
  code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = "ApiError"
    this.status = status
    this.code = code
  }
}

export type User = {
  id: string
  email: string
  emailVerified: boolean
}

export type Me = {
  user: User | null
  /** A recovery-code session: it may only register a new passkey. */
  recovery: boolean
  csrfToken: string
  googleEnabled: boolean
  mailEnabled: boolean
}

type Init = { method?: string; body?: unknown; signal?: AbortSignal }

const SAFE_METHODS: Record<string, true> = { GET: true, HEAD: true, OPTIONS: true }

export async function api<T>(path: string, init: Init = {}): Promise<T> {
  const method = (init.method ?? "GET").toUpperCase()
  const unsafe = !SAFE_METHODS[method]
  const send = async () => {
    const headers: Record<string, string> = { Accept: "application/json" }
    if (init.body !== undefined) headers["Content-Type"] = "application/json"
    if (unsafe) headers["X-CSRF-Token"] = await csrfToken()
    return fetch(path, {
      method,
      headers,
      body: init.body === undefined ? undefined : JSON.stringify(init.body),
      credentials: "same-origin",
      signal: init.signal,
    })
  }
  let res = await send()
  // Another tab may have replaced the CSRF cookie: refresh the token once.
  if (unsafe && res.status === 403) {
    const err = await toError(res.clone())
    if (err.code === "bad_csrf") {
      csrf = null
      res = await send()
    }
  }
  if (!res.ok) throw await toError(res)
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

async function toError(res: Response): Promise<ApiError> {
  try {
    const body = (await res.json()) as { error?: { code?: string; message?: string } }
    if (body.error?.code) {
      return new ApiError(res.status, body.error.code, body.error.message ?? body.error.code)
    }
  } catch {
    // Not JSON: fall through.
  }
  return new ApiError(res.status, "http_" + res.status, "The server answered " + res.status + ". Try again.")
}

/** A user-facing message for any error thrown around api(). */
export function errorMessage(err: unknown): string {
  if (err instanceof ApiError) return err.message
  if (err instanceof Error && err.name === "NotAllowedError") {
    return "The passkey prompt was dismissed or timed out."
  }
  if (err instanceof Error && err.message) return err.message
  return "Something went wrong. Try again."
}

// --- Session state ---------------------------------------------------------

let csrf: string | null = null
let state: { me: Me | null; loading: boolean } = { me: null, loading: true }
let inflight: Promise<Me> | null = null
const listeners = new Set<() => void>()

function publish(next: typeof state) {
  state = next
  listeners.forEach((l) => l())
}

async function csrfToken(): Promise<string> {
  if (csrf) return csrf
  return (await refreshMe()).csrfToken
}

/** Re-reads GET /app-api/me (after signing in or out) and updates useMe(). */
export function refreshMe(): Promise<Me> {
  inflight ??= (async () => {
    try {
      const res = await fetch("/app-api/me", { credentials: "same-origin", headers: { Accept: "application/json" } })
      if (!res.ok) throw await toError(res)
      const me = (await res.json()) as Me
      csrf = me.csrfToken
      publish({ me, loading: false })
      return me
    } catch (err) {
      publish({ me: state.me, loading: false })
      throw err
    } finally {
      inflight = null
    }
  })()
  return inflight
}

function subscribe(l: () => void) {
  listeners.add(l)
  return () => listeners.delete(l)
}

const serverState = { me: null, loading: true }

/**
 * The signed-in user (null when signed out or in a recovery session), plus
 * which sign-in features the server offers. Loads /app-api/me on first use.
 */
export function useMe() {
  const s = useSyncExternalStore(subscribe, () => state, () => serverState)
  useEffect(() => {
    if (state.loading && !inflight) refreshMe().catch(() => {})
  }, [])
  return {
    user: s.me?.user ?? null,
    loading: s.loading,
    recovery: s.me?.recovery ?? false,
    googleEnabled: s.me?.googleEnabled ?? false,
    mailEnabled: s.me?.mailEnabled ?? false,
  }
}

// --- Redirect targets --------------------------------------------------------

/**
 * Reports whether a ?return= value is a same-origin path: a single leading
 * "/", no "//" prefix, no backslash or control characters (matches
 * auth.SafeReturn on the server).
 */
export function safeReturn(value: string | null | undefined): string | null {
  if (!value || !value.startsWith("/") || value.startsWith("//") || value.includes("\\")) return null
  if (/[\u0000-\u001f\u007f]/.test(value)) return null
  return value
}

/** /login?return=<current path>, for screens that need a signed-in user. */
export function loginHref(returnTo: string): string {
  return safeReturn(returnTo) ? `/login?return=${encodeURIComponent(returnTo)}` : "/login"
}
