import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import type { AdminMetricsResponse } from "@/lib/api/types"

interface AgentStatusProps {
  agent: AdminMetricsResponse["agent"]
}

function display(value: string | number | undefined): string | number {
  return value ?? "Unknown"
}

export function AgentStatus({ agent }: AgentStatusProps) {
  return (
    <Card>
      <CardHeader className="flex-row items-start justify-between">
        <div>
          <CardTitle>Agent 状态</CardTitle>
          <CardDescription>仅展示 API 返回的 Agent 信息</CardDescription>
        </div>
        <Badge variant={agent.online ? "default" : "secondary"}>
          {agent.online ? "Online" : "Offline"}
        </Badge>
      </CardHeader>
      <CardContent>
        <dl className="grid gap-x-8 gap-y-4 sm:grid-cols-2 lg:grid-cols-3">
          <div><dt className="text-sm text-muted-foreground">Enabled</dt><dd className="mt-1 font-medium">{agent.enabled ? "Enabled" : "Disabled"}</dd></div>
          <div><dt className="text-sm text-muted-foreground">Endpoint</dt><dd className="mt-1 truncate font-medium" title={String(display(agent.endpoint))}>{display(agent.endpoint)}</dd></div>
          <div><dt className="text-sm text-muted-foreground">Consumer ID</dt><dd className="mt-1 truncate font-medium" title={String(display(agent.consumerId))}>{display(agent.consumerId)}</dd></div>
          <div><dt className="text-sm text-muted-foreground">Version</dt><dd className="mt-1 font-medium">{display(agent.agentVersion)}</dd></div>
          <div><dt className="text-sm text-muted-foreground">Last seen</dt><dd className="mt-1 font-medium">{display(agent.lastSeen)}</dd></div>
          <div><dt className="text-sm text-muted-foreground">Pending events</dt><dd className="mt-1 font-medium">{display(agent.pendingEvents)}</dd></div>
        </dl>
      </CardContent>
    </Card>
  )
}
