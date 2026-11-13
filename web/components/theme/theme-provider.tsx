"use client"

import { ThemeProvider as NextThemesProvider } from "next-themes"

/** Class-based light/dark theming; follows the OS until the user picks one. */
export function ThemeProvider({ children }: { children: React.ReactNode }) {
  return (
    <NextThemesProvider
      attribute="class"
      defaultTheme="system"
      enableSystem
      disableTransitionOnChange
    >
      {children}
    </NextThemesProvider>
  )
}
