import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useAdminMetrics } from "@/hooks/useAdminMetrics"
import { useConsoleLogs } from "@/hooks/useConsoleLogs"

function formatUptime(seconds: number): string {
  const days = Math.floor(seconds / 86_400)
  const hours = Math.floor((seconds % 86_400) / 3_600)
  const minutes = Math.floor((seconds % 3_600) / 60)
  return `${days}d ${hours}h ${minutes}m ${Math.floor(seconds % 60)}s`
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 ** 2) return `${(bytes / 1024).toFixed(1)} KiB`
  if (bytes < 1024 ** 3) return `${(bytes / 1024 ** 2).toFixed(1)} MiB`
  return `${(bytes / 1024 ** 3).toFixed(1)} GiB`
}

function Metric({ label, value }: { label: string; value: string | number }) {
  return <div className="flex items-baseline justify-between gap-4 border-b border-border/60 py-3 last:border-0"><dt className="text-sm text-muted-foreground">{label}</dt><dd className="text-right text-sm font-medium">{value}</dd></div>
}

function ProcessCard({ metrics }: { metrics: NonNullable<ReturnType<typeof useAdminMetrics>["data"]> }) {
  const { process, cpu, memory } = metrics
  return <Card><CardHeader><CardTitle>服务进程</CardTitle><CardDescription>当前服务进程和 runtime 指标</CardDescription></CardHeader><CardContent><dl><Metric label="PID" value={process.pid} /><Metric label="Go version" value={process.goVersion} /><Metric label="Runtime" value={process.runtime} /><Metric label="Platform" value={process.platform} /><Metric label="Architecture" value={process.arch} /><Metric label="Uptime" value={formatUptime(process.uptime)} /><Metric label="Goroutines" value={process.goroutines} /><Metric label="CPU" value={`${cpu.percent}% (${cpu.cores} cores)`} /><Metric label="Memory" value={formatBytes(memory.rss)} /><Metric label="Heap" value={`${formatBytes(memory.heapUsed)} / ${formatBytes(memory.heapTotal)}`} /></dl></CardContent></Card>
}

function AgentCard({ agent }: { agent: NonNullable<ReturnType<typeof useAdminMetrics>["data"]>["agent"] }) {
  const display = (value: string | number | undefined): string | number => value ?? "Unknown"
  return <Card><CardHeader className="flex-row items-start justify-between"><div><CardTitle>Agent 状态</CardTitle><CardDescription>仅展示 metrics.agent 返回的数据</CardDescription></div><Badge variant={agent.online ? "default" : "secondary"}>{agent.online ? "Online" : "Offline"}</Badge></CardHeader><CardContent><dl className="grid gap-x-8 gap-y-4 sm:grid-cols-2"><div><dt className="text-sm text-muted-foreground">Enabled</dt><dd className="mt-1 font-medium">{agent.enabled ? "Enabled" : "Disabled"}</dd></div><div><dt className="text-sm text-muted-foreground">Online</dt><dd className="mt-1 font-medium">{agent.online ? "Online" : "Offline"}</dd></div><div><dt className="text-sm text-muted-foreground">Endpoint</dt><dd className="mt-1 truncate font-medium" title={String(display(agent.endpoint))}>{display(agent.endpoint)}</dd></div><div><dt className="text-sm text-muted-foreground">Consumer ID</dt><dd className="mt-1 truncate font-medium" title={String(display(agent.consumerId))}>{display(agent.consumerId)}</dd></div><div><dt className="text-sm text-muted-foreground">Agent version</dt><dd className="mt-1 font-medium">{display(agent.agentVersion)}</dd></div><div><dt className="text-sm text-muted-foreground">Last seen</dt><dd className="mt-1 font-medium">{display(agent.lastSeen)}</dd></div><div><dt className="text-sm text-muted-foreground">Pending events</dt><dd className="mt-1 font-medium">{display(agent.pendingEvents)}</dd></div></dl></CardContent></Card>
}

export function SystemPage() {
  const metrics = useAdminMetrics()
  const logs = useConsoleLogs()

  return <section aria-labelledby="system-title" className="flex flex-col gap-6">
    <div><p className="text-sm font-medium text-cyan-700">Runtime observability</p><h1 id="system-title" className="mt-1 text-3xl font-semibold tracking-tight text-slate-950">系统状态</h1><p className="mt-2 text-sm text-slate-500">服务进程、Agent 和 Console 实时状态。</p></div>
    {metrics.loading && <div className="grid gap-4 xl:grid-cols-2"><Skeleton className="h-[520px] rounded-xl" /><Skeleton className="h-[360px] rounded-xl" /></div>}
    {!metrics.loading && metrics.error && <Alert variant="destructive"><AlertTitle>无法加载系统指标</AlertTitle><AlertDescription>{metrics.error instanceof Error ? metrics.error.message : "API 返回错误"}</AlertDescription></Alert>}
    {!metrics.loading && !metrics.error && !metrics.data && <Alert><AlertTitle>暂无系统指标</AlertTitle><AlertDescription>服务端尚未返回系统指标数据。</AlertDescription></Alert>}
    {!metrics.loading && !metrics.error && metrics.data && <div className="grid gap-4 xl:grid-cols-2"><ProcessCard metrics={metrics.data} /><AgentCard agent={metrics.data.agent} /></div>}

    <Card><CardHeader><CardTitle>Console</CardTitle><CardDescription>日志初始数据来自 API，实时日志由 WebSocketProvider 更新。</CardDescription></CardHeader><CardContent>
      {logs.loading && <div className="flex flex-col gap-2">{["one", "two", "three", "four"].map((line) => <Skeleton key={line} className="h-9 w-full" />)}</div>}
      {!logs.loading && logs.error && <Alert variant="destructive"><AlertTitle>无法加载 Console 日志</AlertTitle><AlertDescription>{logs.error instanceof Error ? logs.error.message : "API 返回错误"}</AlertDescription></Alert>}
      {!logs.loading && !logs.error && !logs.data && <Alert><AlertTitle>暂无 Console 数据</AlertTitle><AlertDescription>服务端尚未返回 Console 日志。</AlertDescription></Alert>}
      {!logs.loading && !logs.error && logs.data && logs.data.lines.length === 0 && <Alert><AlertTitle>暂无日志</AlertTitle><AlertDescription>当前没有可展示的 Console 日志。</AlertDescription></Alert>}
      {!logs.loading && !logs.error && logs.data && logs.data.lines.length > 0 && <div className="flex max-h-[520px] flex-col gap-2 overflow-auto rounded-lg bg-slate-950 p-3">{logs.data.lines.map((line, index) => <div key={`${line.timestamp}-${index}`} className="grid gap-2 border-b border-slate-800 pb-2 text-sm last:border-0 sm:grid-cols-[100px_160px_1fr]"><Badge variant="outline" className="w-fit border-slate-700 text-slate-300">{line.level}</Badge><span className="text-slate-500">{line.timestamp}</span><span className="break-words text-slate-200">{line.message}</span></div>)}</div>}
    </CardContent></Card>
  </section>
}
