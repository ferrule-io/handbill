"use client"

import { useSyncExternalStore } from "react"

/**
 * UI routes are fixed paths; parameters travel in the query string or the
 * fragment (docs/design.md, "Frontend"). These helpers are the only place
 * that parses them.
 */

/** Site ids: 12 characters of lowercase base32 (a-z, 2-7). */
export const SITE_ID_PATTERN = /^[a-z2-7]{12}$/

export function isSiteId(value: string | null | undefined): value is string {
  return !!value && SITE_ID_PATTERN.test(value)
}

/** Reads one key from a `#a=b&c=d` fragment. */
export function fragmentParam(hash: string, key: string): string | null {
  return new URLSearchParams(hash.replace(/^#/, "")).get(key)
}

function subscribeHash(onChange: () => void) {
  // Client-side navigation updates the URL with history.pushState, which
  // fires neither event; Next re-renders the page, which re-reads the snapshot.
  window.addEventListener("hashchange", onChange)
  window.addEventListener("popstate", onChange)
  return () => {
    window.removeEventListener("hashchange", onChange)
    window.removeEventListener("popstate", onChange)
  }
}

/**
 * The current `location.hash`; null during prerender, so pages render their
 * loading state into the static HTML and fill in on the client.
 */
export function useHash(): string | null {
  return useSyncExternalStore(
    subscribeHash,
    () => window.location.hash,
    () => null
  )
}
