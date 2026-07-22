import type { ReactNode } from "react"

import { Header } from "@/components/Header"
import { Sidebar } from "@/components/Sidebar"

interface AppShellProps {
  children: ReactNode
}

export function AppShell({ children }: AppShellProps) {
  return (
    <div className="min-h-screen bg-slate-100 text-slate-900 md:flex">
      <Sidebar />
      <div className="min-w-0 flex-1">
        <Header />
        <main className="mx-auto w-full max-w-[1600px] p-5 md:p-8">{children}</main>
      </div>
    </div>
  )
}
