import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import type { AdminMetricsResponse } from "@/lib/api/types"

interface MetricsOverviewProps {
  metrics: AdminMetricsResponse
}

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86_400)
  const hours = Math.floor((seconds % 86_400) / 3_600)
  const minutes = Math.floor((seconds % 3_600) / 60)
  const remainingSeconds = Math.floor(seconds % 60)
  return `${days}d ${hours}h ${minutes}m ${remainingSeconds}s`
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KiB`
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MiB`
  return `${(bytes / 1024 ** 3).toFixed(1)} GiB`
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="flex items-baseline justify-between gap-4 border-b border-border/60 py-3 last:border-0">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="text-right text-sm font-medium text-foreground">{value}</dd>
    </div>
  )
}

export function MetricsOverview({ metrics }: MetricsOverviewProps) {
  const { process, server, cpu, memory } = metrics

  return (
    <div className="grid gap-4 xl:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>服务状态概览</CardTitle>
          <CardDescription>来自当前服务进程的运行信息</CardDescription>
        </CardHeader>
        <CardContent>
          <dl>
            <Metric label="Server name" value={server.name} />
            <Metric label="Server version" value={server.version} />
            <Metric label="Process PID" value={process.pid} />
            <Metric label="Go version" value={process.goVersion} />
            <Metric label="Runtime" value={process.runtime} />
            <Metric label="Platform" value={process.platform} />
            <Metric label="Architecture" value={process.arch} />
            <Metric label="Uptime" value={formatUptime(process.uptime)} />
          </dl>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>资源指标</CardTitle>
          <CardDescription>当前 CPU、内存和 Go runtime 指标</CardDescription>
        </CardHeader>
        <CardContent>
          <dl>
            <Metric label="CPU 使用率" value={`${cpu.percent}%`} />
            <Metric label="CPU 核心数" value={cpu.cores} />
            <Metric label="内存使用" value={formatBytes(memory.rss)} />
            <Metric label="Heap 使用" value={`${formatBytes(memory.heapUsed)} / ${formatBytes(memory.heapTotal)}`} />
            <Metric label="GC 次数" value={memory.numGC} />
            <Metric label="Goroutine 数量" value={process.goroutines} />
          </dl>
        </CardContent>
      </Card>
    </div>
  )
}
