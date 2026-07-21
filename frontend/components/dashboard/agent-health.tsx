import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { cn } from "@/lib/utils"

export type Agent = {
  name: string
  role: string
  load: number
  status: "healthy" | "degraded" | "down"
}

const dotFor = (s: Agent["status"]) =>
  s === "healthy" ? "bg-success" : s === "degraded" ? "bg-warning" : "bg-destructive"

const barFor = (s: Agent["status"]) =>
  s === "healthy" ? "bg-success" : s === "degraded" ? "bg-warning" : "bg-destructive"

export function AgentHealth({ agents }: { agents: Agent[] }) {
  return (
    <Card className="h-full">
      <CardHeader>
        <CardTitle className="text-base">Agent Health</CardTitle>
        <CardDescription>Worker pool utilization and status</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {agents.length === 0 ? <p className="text-sm text-muted-foreground">No worker data reported by the Agent.</p> : agents.map((a) => (
          <div key={a.name} className="flex flex-col gap-2">
            <div className="flex items-center justify-between">
              <div className="flex items-center gap-2">
                <span className={cn("size-2 rounded-full", dotFor(a.status))} />
                <span className="font-mono text-xs">{a.name}</span>
                <span className="text-xs text-muted-foreground">· {a.role}</span>
              </div>
              <span className="font-mono text-xs tabular-nums text-muted-foreground">
                {a.load}%
              </span>
            </div>
            <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className={cn("h-full rounded-full", barFor(a.status))}
                style={{ width: `${a.load}%` }}
              />
            </div>
          </div>
        ))}
      </CardContent>
    </Card>
  )
}
