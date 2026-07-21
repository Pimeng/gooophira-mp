import type { Metadata } from "next"
import { Inter, JetBrains_Mono } from "next/font/google"
import "./globals.css"
import { ThemeProvider } from "@/components/theme-provider"
import { AppShell } from "@/components/dashboard/app-shell"

const fontSans = Inter({ subsets: ["latin"], variable: "--font-sans" })
const fontMono = JetBrains_Mono({ subsets: ["latin"], variable: "--font-mono" })

export const metadata: Metadata = {
  title: "GoooPhira MP · Admin Console",
  description:
    "Operations console for the GoooPhira MP multiplayer game server — realtime monitoring, runtime config, and service health.",
}

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode
}>) {
  return (
    <html
      lang="en"
      suppressHydrationWarning
      className={`dark bg-background ${fontSans.variable} ${fontMono.variable}`}
    >
      <body className="antialiased">
        <ThemeProvider defaultTheme="dark" forcedTheme="dark">
          <AppShell>{children}</AppShell>
        </ThemeProvider>
      </body>
    </html>
  )
}
