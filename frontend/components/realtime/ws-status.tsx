"use client"

import * as React from "react"
import { Card, CardContent } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { cn } from "@/lib/utils"

export function WsStatus({
  connected,
  connections,
  messagesPerSecond,
  latency,
  onConnect,
  onDisconnect,
}: {
  connected: boolean
  connections: number
  messagesPerSecond: number
  latency: number
  onConnect: () => void
  onDisconnect: () => void
}) {
  return (
    <Card>
      <CardContent className="flex flex-col gap-5 p-5 lg:flex-row lg:items-center lg:justify-between">
        <div className="flex items-center gap-3">
          <span className="relative flex size-3">
            {connected ? <span className="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-75" /> : null}
            <span className={cn("relative inline-flex size-3 rounded-full", connected ? "bg-success" : "bg-muted-foreground")} />
          </span>
          <div>
            <div className="flex items-center gap-2">
              <h2 className="font-semibold">WebSocket connection</h2>
              <Badge variant={connected ? "default" : "secondary"} className={connected ? "bg-success/15 text-success" : ""}>
                {connected ? "CONNECTED" : "DISCONNECTED"}
              </Badge>
            </div>
            <p className="mt-1 font-mono text-xs text-muted-foreground">WS /ws</p>
          </div>
        </div>
        <div className="grid grid-cols-3 gap-5 sm:flex sm:items-center sm:gap-8">
          <Metric label="Connections" value={connections.toLocaleString()} />
          <Metric label="Messages / sec" value={messagesPerSecond.toLocaleString()} />
          <Metric label="Avg. latency" value={`${latency} ms`} />
          <div className="col-span-3 flex gap-2 sm:col-span-1">
            <Button size="sm" onClick={onConnect} disabled={connected}>Connect</Button>
            <Button size="sm" variant="outline" onClick={onDisconnect} disabled={!connected}>Disconnect</Button>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}

function Metric({ label, value }: { label: string; value: string }) {
  return <div><p className="text-xs text-muted-foreground">{label}</p><p className="mt-1 font-mono text-lg font-semibold tabular-nums">{value}</p></div>
}
