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
import { useAdminRooms } from "@/hooks/useAdminRooms"

function RoomStatus({ label, active }: { label: string; active: boolean }) {
  return <Badge variant={active ? "default" : "secondary"}>{label}: {active ? "是" : "否"}</Badge>
}

export function RoomsPage() {
  const { data, loading, error } = useAdminRooms()

  return (
    <section aria-labelledby="rooms-title" className="flex flex-col gap-6">
      <div>
        <p className="text-sm font-medium text-cyan-700">Live rooms</p>
        <h1 id="rooms-title" className="mt-1 text-3xl font-semibold tracking-tight text-slate-950">房间列表</h1>
        <p className="mt-2 text-sm text-slate-500">实时查看当前房间、房主、谱面与比赛状态。</p>
      </div>

      {loading && (
        <Card>
          <CardHeader><Skeleton className="h-6 w-32" /><Skeleton className="h-4 w-64" /></CardHeader>
          <CardContent className="flex flex-col gap-3">
            {["one", "two", "three", "four"].map((row) => <Skeleton key={row} className="h-12 w-full" />)}
          </CardContent>
        </Card>
      )}

      {!loading && error && (
        <Alert variant="destructive">
          <AlertTitle>无法加载房间列表</AlertTitle>
          <AlertDescription>{error instanceof Error ? error.message : "API 返回错误"}</AlertDescription>
        </Alert>
      )}

      {!loading && !error && !data && (
        <Alert><AlertTitle>暂无房间数据</AlertTitle><AlertDescription>服务端尚未返回房间列表。</AlertDescription></Alert>
      )}

      {!loading && !error && data && data.rooms.length === 0 && (
        <Alert><AlertTitle>当前没有房间</AlertTitle><AlertDescription>服务端当前返回的房间列表为空。</AlertDescription></Alert>
      )}

      {!loading && !error && data && data.rooms.length > 0 && (
        <Card>
          <CardHeader>
            <CardTitle>房间</CardTitle>
            <CardDescription>共 {data.rooms.length} 个当前房间</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>房间</TableHead><TableHead>状态</TableHead><TableHead>人数</TableHead>
                    <TableHead>房主</TableHead><TableHead>谱面</TableHead><TableHead>比赛</TableHead><TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.rooms.map((room) => (
                    <TableRow key={room.roomid}>
                      <TableCell className="font-medium">{room.roomid}</TableCell>
                      <TableCell><div className="flex max-w-48 flex-wrap gap-1"><RoomStatus label="Live" active={room.live} /><RoomStatus label="Locked" active={room.locked} /><RoomStatus label="Cycle" active={room.cycle} /></div></TableCell>
                      <TableCell>{room.current_users} / {room.max_users}<div className="text-xs text-muted-foreground">观战 {room.current_monitors}</div></TableCell>
                      <TableCell><div>{room.host.name}</div><div className="text-xs text-muted-foreground">#{room.host.id} · {room.host.connected ? "在线" : "离线"}</div></TableCell>
                      <TableCell>{room.chart ? <><div>{room.chart.name}</div><div className="text-xs text-muted-foreground">#{room.chart.id}</div></> : "Unknown"}</TableCell>
                      <TableCell>{room.contest ? <Badge variant="outline">Whitelist {room.contest.whitelist_count}</Badge> : "Unknown"}</TableCell>
                      <TableCell className="text-right"><Button asChild variant="outline" size="sm"><Link to={`/rooms/${encodeURIComponent(room.roomid)}`}>查看详情</Link></Button></TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>
      )}
    </section>
  )
}
