"use client"

import * as React from "react"
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Badge } from "@/components/ui/badge"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs"

export type ConsoleLog = { id: number; level: "INFO" | "WARN" | "ERROR"; text: string; timestamp: string }

export function ConsoleStream({ connected, logs, onClear }: { connected: boolean; logs: ConsoleLog[]; onClear: () => void }) {
  const [level, setLevel] = React.useState<ConsoleLog["level"] | "ALL">("ALL")
  const visible = level === "ALL" ? logs : logs.filter((log) => log.level === level)
  return <Card className="min-w-0 gap-0 py-0"><CardHeader className="gap-3 border-b border-border px-4 py-4"><div className="flex items-center justify-between"><CardTitle className="font-mono text-sm">console_log</CardTitle><Badge variant="outline" className="font-mono text-[0.65rem]">{logs.length} logs</Badge></div><Tabs value={level} onValueChange={(value) => setLevel(value as ConsoleLog["level"] | "ALL")}><TabsList className="h-8 w-full"><TabsTrigger value="ALL" className="text-xs">ALL</TabsTrigger><TabsTrigger value="INFO" className="text-xs">INFO</TabsTrigger><TabsTrigger value="WARN" className="text-xs">WARN</TabsTrigger><TabsTrigger value="ERROR" className="text-xs">ERROR</TabsTrigger></TabsList></Tabs></CardHeader><CardContent className="p-0"><ScrollArea className="h-[360px] bg-zinc-950"><div className="space-y-1 p-3 font-mono text-[0.7rem] leading-relaxed">{visible.length === 0 ? <p className="py-8 text-center text-muted-foreground">{connected ? "Waiting for logs..." : "Stream disconnected"}</p> : null}{visible.map((log) => <div key={log.id} className="flex gap-2"><span className="shrink-0 text-muted-foreground/50">{log.timestamp}</span><span className={log.level === "ERROR" ? "text-destructive" : log.level === "WARN" ? "text-warning" : "text-muted-foreground"}>{log.level.padEnd(5)}</span><span className="break-all text-foreground/85">{log.text}</span></div>)}</div></ScrollArea><div className="flex justify-end border-t border-border px-3 py-2"><Button size="sm" variant="ghost" className="h-7 px-2 text-xs" onClick={onClear}>Clear</Button></div></CardContent></Card>
}
