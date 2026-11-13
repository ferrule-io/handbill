"use client"

import { LogOutIcon, ShieldIcon } from "lucide-react"
import Link from "next/link"
import { usePathname } from "next/navigation"
import { useEffect, useState } from "react"
import { toast } from "sonner"

import { signOut } from "@/components/auth/ceremonies"
import { Wordmark } from "@/components/brand/wordmark"
import { Container } from "@/components/layout/container"
import { ThemeToggle } from "@/components/theme/theme-toggle"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Skeleton } from "@/components/ui/skeleton"
import { errorMessage, loginHref, useMe, type User } from "@/lib/api"
import { cn } from "@/lib/utils"

const nav = [
  { href: "/", label: "Sites", match: (p: string) => p === "/" || p.startsWith("/site") },
  { href: "/settings/security", label: "Security", match: (p: string) => p.startsWith("/settings") },
]

/**
 * Chrome for signed-in screens: masthead with navigation and the account
 * menu, page body, colophon. Signed-out visitors are sent to /login with a
 * return path; a recovery session goes back to finish registering a passkey.
 */
export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname()
  const { user, loading, recovery } = useMe()

  useEffect(() => {
    if (loading || user) return
    const here = location.pathname + location.search + location.hash
    window.location.replace(recovery ? "/login/recover" : loginHref(here))
  }, [loading, user, recovery])

  return (
    <div className="flex min-h-svh flex-col">
      <a
        href="#main"
        className="sr-only z-50 rounded-md bg-background px-3 py-2 text-sm font-medium shadow-press ring-1 ring-border focus:not-sr-only focus:fixed focus:top-3 focus:left-3"
      >
        Skip to content
      </a>
      <header className="sticky top-0 z-40 border-b border-border/70 bg-background/80 backdrop-blur-md supports-backdrop-filter:bg-background/65">
        <Container size="wide" className="flex h-15 items-center gap-3 sm:gap-6">
          <Link href="/" className="flex items-center rounded-md pointer-coarse:min-h-11" aria-label="Handbill, your sites">
            <Wordmark collapse />
          </Link>
          <nav aria-label="Main" className="flex items-center gap-1">
            {nav.map((item) => {
              const active = item.match(pathname)
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "relative inline-flex items-center rounded-md px-3 py-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground pointer-coarse:min-h-11",
                    active &&
                      "text-foreground after:absolute after:inset-x-3 after:-bottom-[0.7rem] after:h-0.5 after:rounded-full after:bg-brand pointer-coarse:after:-bottom-2"
                  )}
                >
                  {item.label}
                </Link>
              )
            })}
          </nav>
          <div className="ml-auto flex items-center gap-1.5">
            <ThemeToggle />
            {user ? <AccountMenu user={user} /> : <Skeleton className="size-8 rounded-full pointer-coarse:size-11" />}
          </div>
        </Container>
      </header>
      <main id="main" tabIndex={-1} className="flex-1 pb-24 outline-none">
        {user ? children : <PageSkeleton />}
      </main>
      <footer className="border-t border-border/70">
        <Container size="wide" className="flex h-14 items-center justify-between font-mono text-xs text-muted-foreground">
          <span>handbill</span>
          <span>self-hosted output for agents</span>
        </Container>
      </footer>
    </div>
  )
}

function AccountMenu({ user }: { user: User }) {
  const [busy, setBusy] = useState(false)
  const initial = (user.email || "?").charAt(0).toUpperCase()

  async function onSignOut() {
    setBusy(true)
    try {
      // The shell sees the signed-out state and sends the browser to /login.
      await signOut()
    } catch (err) {
      toast.error(errorMessage(err))
      setBusy(false)
    }
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          aria-label="Account"
          data-testid="account-menu"
          className="rounded-full p-0 hover:bg-transparent aria-expanded:bg-transparent"
        >
          <span className="grid size-8 place-items-center rounded-full bg-brand-soft font-heading text-sm font-semibold text-brand-strong ring-1 ring-brand/25">
            {initial}
          </span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuLabel className="flex flex-col gap-0.5 py-2">
          <span className="eyebrow">Signed in as</span>
          <span className="truncate font-mono text-xs font-normal text-foreground" data-testid="account-email">
            {user.email || "Google account"}
          </span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link href="/settings/security">
            <ShieldIcon /> Security settings
          </Link>
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={onSignOut} disabled={busy}>
          <LogOutIcon /> Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

function PageSkeleton() {
  return (
    <Container aria-busy="true" aria-label="Loading">
      <div className="flex flex-col gap-4 pt-10 sm:pt-14">
        <Skeleton className="h-3 w-20" />
        <Skeleton className="h-10 w-72 max-w-full" />
        <Skeleton className="h-4 w-96 max-w-full" />
        <Skeleton className="mt-10 h-32 w-full rounded-2xl" />
      </div>
    </Container>
  )
}
