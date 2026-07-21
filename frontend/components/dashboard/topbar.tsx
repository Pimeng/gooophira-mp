"use client"

import { HugeiconsIcon } from "@hugeicons/react"
import {
  Search01Icon,
  Notification03Icon,
  Menu01Icon,
} from "@hugeicons/core-free-icons"
import { Button } from "@/components/ui/button"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { Avatar, AvatarFallback } from "@/components/ui/avatar"
import { StatusPill } from "./status-pill"

export function Topbar({ onMenu }: { onMenu?: () => void }) {
  return (
    <header className="sticky top-0 z-30 flex h-16 items-center gap-3 border-b border-border bg-background/80 px-4 backdrop-blur md:px-6">
      <Button
        variant="ghost"
        size="icon"
        className="lg:hidden"
        onClick={onMenu}
        aria-label="Toggle navigation"
      >
        <HugeiconsIcon icon={Menu01Icon} strokeWidth={2} />
      </Button>

      <div className="w-full max-w-sm">
        <InputGroup>
          <InputGroupInput placeholder="Search rooms, players, endpoints..." />
          <InputGroupAddon>
            <InputGroupText>
              <HugeiconsIcon icon={Search01Icon} strokeWidth={2} />
            </InputGroupText>
          </InputGroupAddon>
        </InputGroup>
      </div>

      <div className="ml-auto hidden items-center gap-2 md:flex">
        <StatusPill label="HTTP 200" tone="success" />
        <StatusPill label="WS LIVE" tone="success" pulse />
        <StatusPill label="AGENT OK" tone="success" />
      </div>

      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              size="icon"
              className="relative"
              aria-label="Notifications"
            >
              <HugeiconsIcon icon={Notification03Icon} strokeWidth={2} />
              <span className="absolute right-2 top-2 size-2 rounded-full bg-destructive" />
            </Button>
          }
        />
        <DropdownMenuContent align="end" className="w-72">
          <DropdownMenuLabel>Notifications</DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem className="flex-col items-start gap-0.5">
              <span className="text-sm">Room throughput spike</span>
              <span className="text-xs text-muted-foreground">
                room_update +42% · 2m ago
              </span>
            </DropdownMenuItem>
            <DropdownMenuItem className="flex-col items-start gap-0.5">
              <span className="text-sm">Agent worker recovered</span>
              <span className="text-xs text-muted-foreground">
                worker-03 back online · 11m ago
              </span>
            </DropdownMenuItem>
            <DropdownMenuItem className="flex-col items-start gap-0.5">
              <span className="text-sm">Feishu webhook delivered</span>
              <span className="text-xs text-muted-foreground">
                alert channel · 26m ago
              </span>
            </DropdownMenuItem>
          </DropdownMenuGroup>
        </DropdownMenuContent>
      </DropdownMenu>

      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="ghost"
              className="h-10 gap-2 px-1.5 pr-2"
              aria-label="User menu"
            >
              <Avatar className="size-7">
                <AvatarFallback className="bg-sidebar-accent text-xs">
                  OP
                </AvatarFallback>
              </Avatar>
              <span className="hidden text-sm sm:inline">ops@goophira</span>
            </Button>
          }
        />
        <DropdownMenuContent align="end" className="w-52">
          <DropdownMenuLabel>Signed in as ops</DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem>Account settings</DropdownMenuItem>
            <DropdownMenuItem>API tokens</DropdownMenuItem>
            <DropdownMenuItem>Audit log</DropdownMenuItem>
          </DropdownMenuGroup>
          <DropdownMenuSeparator />
          <DropdownMenuItem variant="destructive">Sign out</DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </header>
  )
}
