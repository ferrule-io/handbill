import type { Metadata, Viewport } from "next"

import "@fontsource-variable/fraunces/full.css"
import "@fontsource-variable/instrument-sans"
import "@fontsource-variable/jetbrains-mono"
import "./globals.css"

import { ThemeProvider } from "@/components/theme/theme-provider"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"

export const metadata: Metadata = {
  title: { default: "agent-dash", template: "%s · agent-dash" },
  description: "Self-hosted static sites published by your agents.",
  robots: { index: false, follow: false },
}

export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#faf6ef" },
    { media: "(prefers-color-scheme: dark)", color: "#14171f" },
  ],
}

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" suppressHydrationWarning>
      <body>
        <ThemeProvider>
          <TooltipProvider>{children}</TooltipProvider>
          <Toaster />
        </ThemeProvider>
      </body>
    </html>
  )
}
