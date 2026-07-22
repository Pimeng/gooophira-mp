import { AgentStatus } from "@/components/dashboard/AgentStatus"
import { BusinessStatus } from "@/components/dashboard/BusinessStatus"
import { MetricsOverview } from "@/components/dashboard/MetricsOverview"
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Skeleton } from "@/components/ui/skeleton"
import { useAdminMetrics } from "@/hooks/useAdminMetrics"

function DashboardLoading() {
  return (
    <div className="flex flex-col gap-4" aria-label="正在加载仪表盘">
      <div className="grid gap-4 xl:grid-cols-2">
        {["service", "resources"].map((section) => (
          <Skeleton key={section} className="h-[390px] rounded-xl" />
        ))}
      </div>
      <Skeleton className="h-64 rounded-xl" />
      <Skeleton className="h-56 rounded-xl" />
    </div>
  )
}

export function DashboardPage() {
  const { data, loading, error } = useAdminMetrics()

  return (
    <section aria-labelledby="dashboard-title" className="flex flex-col gap-6">
      <div>
        <p className="text-sm font-medium text-cyan-700">Live operations</p>
        <h1 id="dashboard-title" className="mt-1 text-3xl font-semibold tracking-tight text-slate-950">仪表盘</h1>
        <p className="mt-2 max-w-2xl text-sm text-slate-500">服务运行状态、资源指标和 Agent 连接信息。</p>
      </div>

      {loading && <DashboardLoading />}

      {!loading && error && (
        <Alert variant="destructive">
          <AlertTitle>无法加载仪表盘</AlertTitle>
          <AlertDescription>{error instanceof Error ? error.message : "API 返回错误"}</AlertDescription>
        </Alert>
      )}

      {!loading && !error && !data && (
        <Alert>
          <AlertTitle>暂无指标数据</AlertTitle>
          <AlertDescription>服务端尚未返回 Dashboard 指标。</AlertDescription>
        </Alert>
      )}

      {!loading && !error && data && (
        <>
          <MetricsOverview metrics={data} />
          <BusinessStatus business={data.business} />
          <AgentStatus agent={data.agent} />
        </>
      )}
    </section>
  )
}
