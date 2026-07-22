import { useParams } from "react-router-dom"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { useAdminUserDetail } from "@/hooks/useAdminUserDetail"

function DetailField({ label, value }: { label: string; value: string | number }) {
  return (
    <div className="rounded-lg border border-border/70 bg-muted/30 p-4">
      <dt className="text-sm text-muted-foreground">{label}</dt>
      <dd className="mt-2 font-medium text-foreground">{value}</dd>
    </div>
  )
}

export function PlayerDetailPage() {
  const { id } = useParams<{ id: string }>()
  const userId = id === undefined || !/^\d+$/.test(id) ? undefined : Number(id)
  const { data, loading, error } = useAdminUserDetail(userId)

  return (
    <section aria-labelledby="player-detail-title" className="flex flex-col gap-6">
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div>
          <p className="text-sm font-medium text-cyan-700">Player detail</p>
          <h1 id="player-detail-title" className="mt-1 text-3xl font-semibold tracking-tight text-slate-950">
            玩家详情
          </h1>
          <p className="mt-2 text-sm text-slate-500">查看服务端返回的单个玩家信息。</p>
        </div>
        <Button variant="outline" onClick={() => window.history.back()}>
          返回
        </Button>
      </div>

      {loading && (
        <Card>
          <CardHeader>
            <Skeleton className="h-6 w-40" />
            <Skeleton className="h-4 w-72" />
          </CardHeader>
          <CardContent className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
            {["id", "name", "monitor", "connected", "room", "banned"].map((field) => (
              <Skeleton key={field} className="h-20 rounded-lg" />
            ))}
          </CardContent>
        </Card>
      )}

      {!loading && userId === undefined && (
        <Alert variant="destructive">
          <AlertTitle>玩家不存在</AlertTitle>
          <AlertDescription>URL 中的玩家 ID 无效。</AlertDescription>
        </Alert>
      )}

      {!loading && userId !== undefined && error && (
        <Alert variant="destructive">
          <AlertTitle>{"status" in error && error.status === 404 ? "玩家不存在" : "无法加载玩家详情"}</AlertTitle>
          <AlertDescription>{error instanceof Error ? error.message : "API 返回错误"}</AlertDescription>
        </Alert>
      )}

      {!loading && userId !== undefined && !error && !data && (
        <Alert>
          <AlertTitle>玩家不存在</AlertTitle>
          <AlertDescription>服务端没有返回玩家 #{userId} 的详情。</AlertDescription>
        </Alert>
      )}

      {!loading && !error && data && (
        <Card>
          <CardHeader>
            <CardTitle>{data.user.name}</CardTitle>
            <CardDescription>服务端玩家 ID #{data.user.id}</CardDescription>
          </CardHeader>
          <CardContent>
            <dl className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
              <DetailField label="ID" value={data.user.id} />
              <DetailField label="名称" value={data.user.name} />
              <DetailField label="角色" value={data.user.monitor ? "Monitor" : "玩家"} />
              <div className="rounded-lg border border-border/70 bg-muted/30 p-4"><dt className="text-sm text-muted-foreground">连接状态</dt><dd className="mt-2"><Badge variant={data.user.connected ? "default" : "secondary"}>{data.user.connected ? "已连接" : "未连接"}</Badge></dd></div>
              <DetailField label="房间" value={data.user.room ?? "未加入房间"} />
              <div className="rounded-lg border border-border/70 bg-muted/30 p-4"><dt className="text-sm text-muted-foreground">封禁状态</dt><dd className="mt-2"><Badge variant={data.user.banned ? "destructive" : "secondary"}>{data.user.banned ? "已封禁" : "正常"}</Badge></dd></div>
            </dl>
          </CardContent>
        </Card>
      )}
    </section>
  )
}
