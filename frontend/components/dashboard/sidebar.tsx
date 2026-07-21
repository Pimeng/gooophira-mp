"use client"

import Link from "next/link"
import { usePathname } from "next/navigation"
import { HugeiconsIcon } from "@hugeicons/react"
import { RoboticIcon } from "@hugeicons/core-free-icons"
import { cn } from "@/lib/utils"
import { navItems } from "./nav-items"

export function Sidebar({ className }: { className?: string }) {
  const pathname = usePathname()

  return (
    <aside
      className={cn(
        "flex h-full w-64 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground",
        className,
      )}
    >
      <div className="flex h-16 items-center gap-3 border-b border-sidebar-border px-5">
        <div className="flex size-9 items-center justify-center rounded-lg bg-sidebar-primary text-sidebar-primary-foreground">
          <HugeiconsIcon icon={RoboticIcon} strokeWidth={2} className="size-5" />
        </div>
        <div className="flex flex-col leading-tight">
          <span className="font-mono text-sm font-semibold tracking-tight">
            GoooPhira MP
          </span>
          <span className="text-xs text-muted-foreground">Admin Console</span>
        </div>
      </div>

      <nav className="flex flex-1 flex-col gap-1 overflow-y-auto p-3">
        <span className="px-3 pb-1 pt-2 text-[0.65rem] font-medium uppercase tracking-wider text-muted-foreground">
          Operations
        </span>
        {navItems.map((item) => {
          const active =
            item.href === "/"
              ? pathname === "/"
              : pathname.startsWith(item.href)
          return (
            <Link
              key={item.href}
              href={item.href}
              aria-current={active ? "page" : undefined}
              className={cn(
                "group flex items-center gap-3 rounded-lg px-3 py-2 text-sm transition-colors",
                active
                  ? "bg-sidebar-accent font-medium text-sidebar-accent-foreground"
                  : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground",
              )}
            >
              <HugeiconsIcon
                icon={item.icon}
                strokeWidth={2}
                className={cn(
                  "size-[1.15rem] shrink-0",
                  active ? "text-sidebar-primary" : "",
                )}
              />
              <span className="flex-1 truncate">{item.label}</span>
              {item.badge ? (
                <span className="flex items-center gap-1 rounded-full bg-success/15 px-2 py-0.5 font-mono text-[0.6rem] font-semibold text-success">
                  <span className="size-1.5 animate-pulse rounded-full bg-success" />
                  {item.badge}
                </span>
              ) : null}
            </Link>
          )
        })}
      </nav>

      <div className="border-t border-sidebar-border p-3">
        <div className="flex items-center gap-3 rounded-lg bg-sidebar-accent/50 px-3 py-2.5">
          <div className="flex flex-col leading-tight">
            <span className="font-mono text-xs text-sidebar-foreground">
              v2.8.1 · prod
            </span>
            <span className="text-[0.65rem] text-muted-foreground">
              region: ap-southeast-1
            </span>
          </div>
          <span className="ml-auto size-2 rounded-full bg-success" />
        </div>
      </div>
    </aside>
  )
}
