import { Link, useParams } from "react-router-dom"

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useRoomDetail } from "@/hooks/useRoomDetail"
import type { AdminRoom, AdminRoomUser, RoomLogLine } from "@/lib/api/types"
import type { RoomUpdateData } from "@/lib/ws/protocol"

type RoomDetail = AdminRoom | RoomUpdateData

function isAdminRoom(room: RoomDetail): room is AdminRoom {
  return "max_users" in room
}

function RoomState({ room }: { room: RoomDetail }) {
  const state = typeof room.state === "string" ? room.state : room.state.type
  return <div className="flex flex-wrap gap-2"><Badge variant="outline">状态: {state}</Badge><Badge variant={room.live ? "default" : "secondary"}>Live: {room.live ? "是" : "否"}</Badge><Badge variant={room.locked ? "default" : "secondary"}>Locked: {room.locked ? "是" : "否"}</Badge><Badge variant={room.cycle ? "default" : "secondary"}>Cycle: {room.cycle ? "是" : "否"}</Badge></div>
}

function UserTable({ users }: { users: AdminRoomUser[] | RoomUpdateData["users"] }) {
  return <Table><TableHeader><TableRow><TableHead>ID</TableHead><TableHead>名称</TableHead><TableHead>连接</TableHead><TableHead>状态</TableHead></TableRow></TableHeader><TableBody>{users.map((user) => <TableRow key={user.id}><TableCell>{user.id}</TableCell><TableCell>{user.name}</TableCell><TableCell>{user.connected ? "在线" : "离线"}</TableCell><TableCell>{"is_ready" in user ? (user.is_ready ? "Ready" : "Not ready") : (user.finished ? "Finished" : "进行中")}</TableCell></TableRow>)}</TableBody></Table>
}

export function RoomDetailPage() {
  const { id } = useParams<{ id: string }>()
  const roomId = id === undefined ? undefined : decodeURIComponent(id)
  const { data, loading, error } = useRoomDetail(roomId)

  return <section aria-labelledby="room-detail-title" className="flex flex-col gap-6">
    <div className="flex flex-wrap items-start justify-between gap-4"><div><p className="text-sm font-medium text-cyan-700">Room detail</p><h1 id="room-detail-title" className="mt-1 text-3xl font-semibold tracking-tight text-slate-950">{roomId ?? "房间详情"}</h1></div><Button asChild variant="outline"><Link to="/rooms">返回房间列表</Link></Button></div>
    {loading && <div className="flex flex-col gap-4"><Skeleton className="h-32 rounded-xl" /><Skeleton className="h-64 rounded-xl" /><Skeleton className="h-48 rounded-xl" /></div>}
    {!loading && error && <Alert variant="destructive"><AlertTitle>无法加载房间详情</AlertTitle><AlertDescription>{error instanceof Error ? error.message : "API 返回错误"}</AlertDescription></Alert>}
    {!loading && !error && !data && <Alert><AlertTitle>房间不存在或暂无数据</AlertTitle><AlertDescription>服务端没有返回房间 `{roomId}` 的数据。</AlertDescription></Alert>}
    {!loading && !error && data && <>
      <Card><CardHeader><CardTitle>房间基础信息</CardTitle><CardDescription>当前房间的服务端状态</CardDescription></CardHeader><CardContent className="flex flex-col gap-4"><RoomState room={data} /><dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4"><div><dt className="text-sm text-muted-foreground">房主</dt><dd className="mt-1 font-medium">{data.host.name} (#{data.host.id})</dd></div><div><dt className="text-sm text-muted-foreground">用户数</dt><dd className="mt-1 font-medium">{isAdminRoom(data) ? `${data.current_users} / ${data.max_users}` : data.users.length}</dd></div><div><dt className="text-sm text-muted-foreground">Monitor 数</dt><dd className="mt-1 font-medium">{isAdminRoom(data) ? data.current_monitors : data.monitors.length}</dd></div><div><dt className="text-sm text-muted-foreground">谱面</dt><dd className="mt-1 font-medium">{data.chart?.name ?? "Unknown"}</dd></div></dl></CardContent></Card>
      <div className="grid gap-4 xl:grid-cols-2"><Card><CardHeader><CardTitle>当前用户</CardTitle><CardDescription>{data.users.length} 位用户</CardDescription></CardHeader><CardContent><UserTable users={data.users} /></CardContent></Card><Card><CardHeader><CardTitle>Monitor 列表</CardTitle><CardDescription>{data.monitors.length} 位 Monitor</CardDescription></CardHeader><CardContent><Table><TableHeader><TableRow><TableHead>ID</TableHead><TableHead>名称</TableHead><TableHead>连接</TableHead></TableRow></TableHeader><TableBody>{data.monitors.map((monitor) => <TableRow key={monitor.id}><TableCell>{monitor.id}</TableCell><TableCell>{monitor.name}</TableCell><TableCell>{monitor.connected ? "在线" : "离线"}</TableCell></TableRow>)}</TableBody></Table></CardContent></Card></div>
      <Card><CardHeader><CardTitle>最近日志</CardTitle><CardDescription>服务端返回的最近房间日志</CardDescription></CardHeader><CardContent><div className="flex flex-col gap-2">{data.recent_logs.length === 0 ? <p className="text-sm text-muted-foreground">暂无日志</p> : data.recent_logs.map((log: RoomLogLine, index) => <div key={`${log.timestamp}-${index}`} className="rounded-md bg-muted/50 px-3 py-2 text-sm"><span className="mr-3 text-xs text-muted-foreground">{log.timestamp}</span>{log.message}</div>)}</div></CardContent></Card>
    </>}
  </section>
}
