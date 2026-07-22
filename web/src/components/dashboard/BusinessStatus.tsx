import { Badge } from "@/components/ui/badge"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import type { AdminMetricsResponse } from "@/lib/api/types"

interface BusinessStatusProps {
  business: AdminMetricsResponse["business"]
}

function BooleanBadge({ value }: { value: boolean }) {
  return <Badge variant={value ? "default" : "secondary"}>{value ? "Enabled" : "Disabled"}</Badge>
}

function BusinessMetric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-lg border border-border/70 bg-muted/30 p-4">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="mt-2 text-2xl font-semibold tracking-tight text-foreground">{value}</dd>
    </div>
  )
}

export function BusinessStatus({ business }: BusinessStatusProps) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>业务状态</CardTitle>
        <CardDescription>来自服务端业务指标的实时概览</CardDescription>
      </CardHeader>
      <CardContent>
        <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <BusinessMetric label="在线用户" value={business.onlineUsers} />
          <BusinessMetric label="活跃房间" value={business.activeRooms} />
          <BusinessMetric label="WebSocket 连接" value={business.wsConnections} />
          <BusinessMetric label="活跃 Session" value={business.activeSessions} />
        </dl>
        <div className="mt-5 flex flex-wrap gap-3 border-t border-border/70 pt-5">
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            房间创建 <BooleanBadge value={business.roomCreationEnabled} />
          </div>
          <div className="flex items-center gap-2 text-sm text-muted-foreground">
            Replay <BooleanBadge value={business.replayEnabled} />
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
