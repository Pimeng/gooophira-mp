import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { StatusPill } from "./status-pill"
import { EndpointBadge } from "./page-header"

export type Room = {
  id: string
  mode: string
  players: string
  region: string
  latency: string
  status: "active" | "filling" | "closing"
}

const toneFor = (s: Room["status"]) =>
  s === "active" ? "success" : s === "filling" ? "warning" : "destructive"

export function RecentRoomsTable({ rooms }: { rooms: Room[] }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">Recent Rooms</CardTitle>
        <CardDescription>
          Live game sessions from <EndpointBadge method="GET" path="/admin/rooms" />
        </CardDescription>
      </CardHeader>
      <CardContent className="px-0">
        <Table className="text-sm">
          <TableHeader>
            <TableRow className="border-border hover:bg-transparent">
              <TableHead className="h-9 px-6 font-mono text-[0.7rem] uppercase tracking-wider text-muted-foreground">
                Room ID
              </TableHead>
              <TableHead className="h-9 font-mono text-[0.7rem] uppercase tracking-wider text-muted-foreground">
                Mode
              </TableHead>
              <TableHead className="h-9 font-mono text-[0.7rem] uppercase tracking-wider text-muted-foreground">
                Players
              </TableHead>
              <TableHead className="h-9 font-mono text-[0.7rem] uppercase tracking-wider text-muted-foreground">
                Region
              </TableHead>
              <TableHead className="h-9 font-mono text-[0.7rem] uppercase tracking-wider text-muted-foreground">
                Latency
              </TableHead>
              <TableHead className="h-9 px-6 text-right font-mono text-[0.7rem] uppercase tracking-wider text-muted-foreground">
                Status
              </TableHead>
            </TableRow>
          </TableHeader>
           <TableBody>
            {rooms.length === 0 ? <TableRow><TableCell colSpan={6} className="py-8 text-center text-muted-foreground">No rooms reported.</TableCell></TableRow> : rooms.map((r) => (
              <TableRow key={r.id} className="border-border">
                <TableCell className="px-6 py-2.5 font-mono text-xs text-foreground/90">
                  {r.id}
                </TableCell>
                <TableCell className="py-2.5 font-mono text-xs text-muted-foreground">
                  {r.mode}
                </TableCell>
                <TableCell className="py-2.5 font-mono text-xs tabular-nums">
                  {r.players}
                </TableCell>
                <TableCell className="py-2.5 font-mono text-xs uppercase text-muted-foreground">
                  {r.region}
                </TableCell>
                <TableCell className="py-2.5 font-mono text-xs tabular-nums">
                  {r.latency}
                </TableCell>
                <TableCell className="px-6 py-2.5 text-right">
                  <StatusPill label={r.status} tone={toneFor(r.status)} />
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}
