"use client"

import * as React from "react"
import { PageHeader } from "@/components/dashboard/page-header"
import { WsStatus } from "@/components/realtime/ws-status"
import { EventStream, type AdminEvent, type RoomEvent } from "@/components/realtime/event-stream"
import { ConsoleStream, type ConsoleLog } from "@/components/realtime/console-stream"
import { getAdminToken } from "@/lib/api"

type WireMessage = { type: string; data?: Record<string, unknown>; roomId?: string }
const time = (value?: unknown) => value ? new Date(typeof value === "number" ? value : String(value)).toLocaleTimeString([], { hour12: false }) : new Date().toLocaleTimeString([], { hour12: false })

export default function RealtimePage() {
  const [connected, setConnected] = React.useState(false)
  const [events, setEvents] = React.useState<{ room: RoomEvent[]; admin: AdminEvent[] }>({ room: [], admin: [] })
  const [logs, setLogs] = React.useState<ConsoleLog[]>([])
  const [messages, setMessages] = React.useState(0)
  const [latency, setLatency] = React.useState(0)
  const wsRef = React.useRef<WebSocket | null>(null)
  const idRef = React.useRef(0)
  const pingRef = React.useRef(0)

  const connect = React.useCallback(() => {
    if (wsRef.current && wsRef.current.readyState <= WebSocket.OPEN) return
    const ws = new WebSocket(`${location.origin.replace("http", "ws")}/api/ws`)
    wsRef.current = ws
    ws.onopen = () => { setConnected(true); ws.send(JSON.stringify({ type: "admin_subscribe", token: getAdminToken() })); ws.send(JSON.stringify({ type: "console_subscribe", token: getAdminToken() })) }
    ws.onmessage = (message) => {
      let payload: WireMessage
      try { payload = JSON.parse(message.data) } catch { return }
      setMessages((count) => count + 1)
      if (payload.type === "pong") { setLatency(Date.now() - pingRef.current); return }
      const id = ++idRef.current
      if (payload.type === "room_update") { const data = payload.data || {}; setEvents((current) => ({ ...current, room: [...current.room, { id, roomId: String(data.roomid || payload.roomId || "-"), state: String(data.state || "unknown"), players: Array.isArray(data.users) ? data.users.length : 0, timestamp: time(data.timestamp) }].slice(-50) })); return }
      if (payload.type === "admin_update") { const changes = payload.data?.changes as Record<string, unknown> | undefined; setEvents((current) => ({ ...current, admin: [...current.admin, { id, action: "admin_update", target: `${Number(changes?.total_rooms || 0)} rooms`, actor: "server", timestamp: time(payload.data?.timestamp) }].slice(-50) })); return }
      if (payload.type === "console_log") { const data = payload.data || {}; const raw = String(data.level || "INFO").toUpperCase(); const level: ConsoleLog["level"] = raw === "WARN" || raw === "ERROR" ? raw : "INFO"; setLogs((current) => [...current, { id, level, text: String(data.message || ""), timestamp: time(data.timestamp) }].slice(-100)) }
    }
    ws.onclose = () => { wsRef.current = null; setConnected(false) }
    ws.onerror = () => ws.close()
  }, [])
  const disconnect = React.useCallback(() => { wsRef.current?.close(); wsRef.current = null; setConnected(false) }, [])
  React.useEffect(() => { connect(); const timer = setInterval(() => { if (wsRef.current?.readyState === WebSocket.OPEN) { pingRef.current = Date.now(); wsRef.current.send(JSON.stringify({ type: "ping" })) } }, 5000); return () => { clearInterval(timer); wsRef.current?.close() } }, [connect])
  return <div className="flex flex-col gap-6 p-4 md:p-6 lg:p-8"><PageHeader title="Realtime Monitoring" description="Inspect live WebSocket channels from the game server cluster." /><WsStatus connected={connected} connections={connected ? 1 : 0} messagesPerSecond={messages} latency={latency} onConnect={connect} onDisconnect={disconnect} /><div className="grid grid-cols-1 gap-4 xl:grid-cols-3"><EventStream kind="room_update" events={events.room} connected={connected} onClear={() => setEvents((current) => ({ ...current, room: [] }))} /><EventStream kind="admin_update" events={events.admin} connected={connected} onClear={() => setEvents((current) => ({ ...current, admin: [] }))} /><ConsoleStream connected={connected} logs={logs} onClear={() => setLogs([])} /></div></div>
}
