"use client"

import * as React from "react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { ScrollArea } from "@/components/ui/scroll-area"
import { PauseIcon, PlayIcon, RefreshIcon } from "@hugeicons/core-free-icons"
import { HugeiconsIcon } from "@hugeicons/react"
import { cn } from "@/lib/utils"

export type RoomEvent = { id: number; roomId: string; state: string; players: number; timestamp: string }
export type AdminEvent = { id: number; action: string; target: string; actor: string; timestamp: string }

export function EventStream({ kind, events, connected, onClear }: { kind: "room_update" | "admin_update"; events: (RoomEvent | AdminEvent)[]; connected: boolean; onClear: () => void }) {
  const [paused, setPaused] = React.useState(false)
  return (
    <Card className="min-w-0 gap-0 py-0">
      <CardHeader className="border-b border-border px-4 py-4"><div className="flex items-center justify-between gap-2"><CardTitle className="font-mono text-sm">{kind}</CardTitle><Badge variant="outline" className="font-mono text-[0.65rem]">{events.length} events</Badge></div></CardHeader>
      <CardContent className="p-0"><ScrollArea className="h-[360px] bg-muted/10"><div className={cn("space-y-2 p-3", paused && "opacity-60")}>
        {events.length === 0 ? <p className="py-8 text-center text-xs text-muted-foreground">{connected ? "Waiting for events..." : "Stream disconnected"}</p> : null}
        {events.map((event) => kind === "room_update" ? <RoomRow key={event.id} event={event as RoomEvent} /> : <AdminRow key={event.id} event={event as AdminEvent} />)}
      </div></ScrollArea><div className="flex items-center justify-between border-t border-border px-3 py-2"><span className="font-mono text-[0.65rem] text-muted-foreground">WS /api/ws</span><div className="flex gap-1"><Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={() => setPaused((value) => !value)}><HugeiconsIcon icon={paused ? PlayIcon : PauseIcon} className="size-3.5" />{paused ? "Resume" : "Pause"}</Button><Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={onClear}><HugeiconsIcon icon={RefreshIcon} className="size-3.5" />Clear</Button></div></div></CardContent>
    </Card>
  )
}

function RoomRow({ event }: { event: RoomEvent }) { return <div className="rounded-lg border border-border/70 bg-background/40 p-2.5 font-mono text-xs"><div className="flex items-center justify-between gap-2"><span className="font-semibold text-chart-2">{event.roomId}</span><span className="text-muted-foreground">{event.timestamp}</span></div><div className="mt-2 flex gap-3 text-muted-foreground"><span>state=<b className="font-normal text-foreground">{event.state}</b></span><span>players=<b className="font-normal text-foreground">{event.players}</b></span></div></div> }
function AdminRow({ event }: { event: AdminEvent }) { const tone = event.action.includes("kick") || event.action.includes("close") ? "destructive" : event.action.includes("scale") ? "secondary" : "default"; return <div className="rounded-lg border border-border/70 bg-background/40 p-2.5 text-xs"><div className="flex items-center justify-between gap-2"><Badge variant={tone} className={cn("font-mono text-[0.65rem]", tone === "default" && "bg-chart-2/15 text-chart-2")}>{event.action}</Badge><span className="font-mono text-muted-foreground">{event.timestamp}</span></div><div className="mt-2 grid gap-1 font-mono text-[0.7rem] text-muted-foreground"><span>target=<b className="font-normal text-foreground">{event.target}</b></span><span>actor=<b className="font-normal text-foreground">{event.actor}</b></span></div></div> }
