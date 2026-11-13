"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"

import { Wordmark } from "@/components/brand/wordmark"
import { Container } from "@/components/layout/container"
import { ThemeToggle } from "@/components/theme/theme-toggle"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

const nav = [
  { href: "/", label: "Sites", match: (p: string) => p === "/" || p.startsWith("/site") },
  { href: "/settings/security", label: "Security", match: (p: string) => p.startsWith("/settings") },
]

/** Chrome for signed-in screens: masthead with navigation, page body, colophon. */
export function AppShell({ children }: { children: React.ReactNode }) {
  const pathname = usePathname()
  return (
    <div className="flex min-h-svh flex-col">
      <header className="sticky top-0 z-40 border-b border-border/70 bg-background/80 backdrop-blur-md supports-backdrop-filter:bg-background/65">
        <Container size="wide" className="flex h-15 items-center gap-3 sm:gap-6">
          <Link href="/" className="rounded-md focus-visible:outline-2 focus-visible:outline-offset-4">
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
                    "relative rounded-md px-3 py-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground",
                    active &&
                      "text-foreground after:absolute after:inset-x-3 after:-bottom-[0.7rem] after:h-0.5 after:rounded-full after:bg-brand"
                  )}
                >
                  {item.label}
                </Link>
              )
            })}
          </nav>
          <div className="ml-auto flex items-center gap-1.5">
            <ThemeToggle />
            <Button asChild variant="outline" size="sm">
              <Link href="/login">Sign in</Link>
            </Button>
          </div>
        </Container>
      </header>
      <main className="flex-1 pb-24">{children}</main>
      <footer className="border-t border-border/70">
        <Container size="wide" className="flex h-14 items-center justify-between font-mono text-xs text-muted-foreground">
          <span>agent-dash</span>
          <span>self-hosted output for agents</span>
        </Container>
      </footer>
    </div>
  )
}
