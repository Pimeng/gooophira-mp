import { Link } from "react-router-dom"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { useAdminUsers } from "@/hooks/useAdminUsers"

export function PlayersPage() {
  const { data, loading, error } = useAdminUsers()

  return (
    <section aria-labelledby="players-title" className="flex flex-col gap-6">
      <div>
        <p className="text-sm font-medium text-cyan-700">Live players</p>
        <h1 id="players-title" className="mt-1 text-3xl font-semibold tracking-tight text-slate-950">玩家管理</h1>
        <p className="mt-2 text-sm text-slate-500">查看当前在线用户及其服务端状态。</p>
      </div>

      {loading && (
        <Card>
          <CardHeader>
            <Skeleton className="h-6 w-32" />
            <Skeleton className="h-4 w-64" />
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            {["one", "two", "three", "four", "five"].map((row) => (
              <Skeleton key={row} className="h-12 w-full" />
            ))}
          </CardContent>
        </Card>
      )}

      {!loading && error && (
        <Alert variant="destructive">
          <AlertTitle>无法加载玩家列表</AlertTitle>
          <AlertDescription>{error instanceof Error ? error.message : "API 返回错误"}</AlertDescription>
        </Alert>
      )}

      {!loading && !error && !data && (
        <Alert>
          <AlertTitle>暂无玩家数据</AlertTitle>
          <AlertDescription>服务端尚未返回玩家列表。</AlertDescription>
        </Alert>
      )}

      {!loading && !error && data && data.users.length === 0 && (
        <Alert>
          <AlertTitle>当前没有玩家</AlertTitle>
          <AlertDescription>服务端当前返回的玩家列表为空。</AlertDescription>
        </Alert>
      )}

      {!loading && !error && data && data.users.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>在线玩家</CardTitle>
            <CardDescription>共 {data.users.length} 名服务端返回的用户</CardDescription>
          </CardHeader>
          <CardContent>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>ID</TableHead>
                  <TableHead>名称</TableHead>
                  <TableHead>连接状态</TableHead>
                  <TableHead>角色</TableHead>
                  <TableHead>房间</TableHead>
                  <TableHead>封禁状态</TableHead>
                  <TableHead>语言</TableHead>
                  <TableHead className="text-right">详情</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {data.users.map((user) => (
                  <TableRow key={user.id}>
                    <TableCell className="font-mono text-xs">{user.id}</TableCell>
                    <TableCell className="font-medium">{user.name}</TableCell>
                    <TableCell>
                      <Badge variant={user.connected ? "default" : "secondary"}>
                        {user.connected ? "已连接" : "未连接"}
                      </Badge>
                    </TableCell>
                    <TableCell>
                      <Badge variant={user.monitor ? "outline" : "secondary"}>
                        {user.monitor ? "Monitor" : "玩家"}
                      </Badge>
                    </TableCell>
                    <TableCell>{user.room || "未加入房间"}</TableCell>
                    <TableCell>
                      <Badge variant={user.banned ? "destructive" : "secondary"}>
                        {user.banned ? "已封禁" : "正常"}
                      </Badge>
                    </TableCell>
                    <TableCell>{user.language}</TableCell>
                    <TableCell className="text-right">
                      <Button asChild variant="outline" size="sm">
                        <Link to={`/players/${encodeURIComponent(String(user.id))}`}>查看详情</Link>
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </CardContent>
        </Card>
      )}
    </section>
  )
}
