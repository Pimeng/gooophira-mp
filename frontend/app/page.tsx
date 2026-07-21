"use client"

import * as React from "react"
import {
  UserGroupIcon,
  Home01Icon,
  Message01Icon,
  Alert02Icon,
} from "@hugeicons/core-free-icons"
import { PageHeader } from "@/components/dashboard/page-header"
import { StatCard } from "@/components/dashboard/stat-card"
import { TrendChart } from "@/components/dashboard/trend-chart"
import { RecentRoomsTable } from "@/components/dashboard/recent-rooms-table"
import { AgentHealth } from "@/components/dashboard/agent-health"
import { Button } from "@/components/ui/button"
import { api, type MetricsResponse } from "@/lib/api"
import type { Agent } from "@/components/dashboard/agent-health"
import type { Room } from "@/components/dashboard/recent-rooms-table"

type DashboardState = { metrics: MetricsResponse | null; history: { t: string; players: number; messages: number }[]; rooms: Room[]; error: string | null }

const emptyState: DashboardState = { metrics: null, history: [], rooms: [], error: null }

export default function Page() {
  const [state, setState] = React.useState<DashboardState>(emptyState)
  React.useEffect(() => {
    let active = true
    const load = async () => {
      try {
        const [metrics, roomsResponse] = await Promise.all([
          api.get<MetricsResponse & { history?: Array<{ timestamp?: number; cpuPercent?: number; messages?: number; players?: number }> }>("/admin/metrics?history=1"),
          api.get<{ rooms?: Array<Record<string, unknown>> }>("/admin/rooms"),
        ])
        if (!active) return
        const rooms = (roomsResponse.rooms || []).map((room) => ({
          id: String(room.roomid || room.roomId || "unknown"),
          mode: String((room.chart as { name?: string } | undefined)?.name || "standard"),
          players: `${Number(room.current_users || room.currentUsers || 0)}/${Number(room.max_users || room.maxUsers || 0)}`,
          region: "-",
          latency: "-",
          status: room.locked ? "closing" : room.live ? "active" : "filling",
        } as Room))
        const history = (metrics.history || []).map((item) => ({
          t: item.timestamp ? new Date(item.timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" }) : "now",
          players: item.players || metrics.rooms?.totalPlayers || 0,
          messages: item.messages || metrics.network?.messagesPerSecond || 0,
        }))
        setState({ metrics, history, rooms, error: null })
      } catch (error) { if (active) setState((current) => ({ ...current, error: error instanceof Error ? error.message : "Failed to load metrics" })) }
    }
    load()
    const timer = setInterval(load, 5000)
    return () => { active = false; clearInterval(timer) }
  }, [])
  const metrics = state.metrics
  const agents: Agent[] = (metrics?.agent?.workers || []).map((worker, index) => ({ name: worker.name || `worker-${index + 1}`, role: worker.role || "agent", load: worker.load || 0, status: worker.status === "down" ? "down" : worker.status === "degraded" ? "degraded" : "healthy" }))
  return (
    <div className="flex flex-col gap-6 p-4 md:p-6 lg:p-8">
      <PageHeader
        title="Dashboard"
        description="Realtime overview of the GoooPhira MP game server cluster."
        actions={
          <>
            <Button variant="outline" size="sm">
              Last 24h
            </Button>
            <Button size="sm">Export report</Button>
          </>
        }
        />
      {state.error ? <p className="text-sm text-destructive">{state.error}</p> : null}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <StatCard
          label="Online players"
          value={(metrics?.rooms?.totalPlayers || 0).toLocaleString()}
          icon={UserGroupIcon}
        />
        <StatCard
          label="Active rooms"
          value={(metrics?.rooms?.count || 0).toLocaleString()}
          icon={Home01Icon}
        />
        <StatCard
          label="Message throughput"
          value={(metrics?.network?.messagesPerSecond || 0).toLocaleString()}
          unit="/sec"
          icon={Message01Icon}
        />
        <StatCard
          label="Error rate"
          value={(metrics?.network?.errorRate || 0).toFixed(2)}
          unit="%"
          icon={Alert02Icon}
        />
      </div>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <div className="lg:col-span-2">
          <TrendChart data={state.history} />
        </div>
        <AgentHealth agents={agents} />
      </div>

      <RecentRoomsTable rooms={state.rooms} />
    </div>
  )
}
