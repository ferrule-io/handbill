import type { Metadata, Viewport } from "next"

import "@fontsource-variable/fraunces/full.css"
import "@fontsource-variable/instrument-sans"
import "@fontsource-variable/jetbrains-mono"
import "./globals.css"

import { ThemeProvider } from "@/components/theme/theme-provider"
import { Toaster } from "@/components/ui/sonner"
import { TooltipProvider } from "@/components/ui/tooltip"

export const metadata: Metadata = {
  title: { default: "Handbill", template: "%s · Handbill" },
  description: "Self-hosted static sites published by your agents.",
  robots: { index: false, follow: false },
}

// The paper and ink tokens (app/globals.css) as sRGB.
export const viewport: Viewport = {
  themeColor: [
    { media: "(prefers-color-scheme: light)", color: "#fcf9f2" },
    { media: "(prefers-color-scheme: dark)", color: "#0b0e15" },
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
