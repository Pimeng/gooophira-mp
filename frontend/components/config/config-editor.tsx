"use client"

import * as React from "react"
import { HugeiconsIcon } from "@hugeicons/react"
import {
  FloppyDiskIcon,
  ArrowTurnBackwardIcon,
  Tick02Icon,
  Alert02Icon,
} from "@hugeicons/core-free-icons"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { StatusPill } from "@/components/dashboard/status-pill"
import { cn } from "@/lib/utils"

const SAVED_CONFIG = `{
  "server": {
    "region": "ap-southeast-1",
    "maxRoomsPerNode": 512,
    "tickRateHz": 30,
    "graceShutdownSec": 20
  },
  "matchmaking": {
    "strategy": "skill_bucketed",
    "maxWaitSec": 45,
    "backfill": true
  },
  "limits": {
    "maxPlayersPerRoom": 24,
    "messagesPerSecond": 40,
    "payloadKb": 64
  },
  "feishu": {
    "enabled": true,
    "alertChannel": "ops-alerts",
    "minSeverity": "warning"
  }
}`

type DiffLine = {
  type: "same" | "add" | "remove"
  text: string
}

function computeDiff(a: string, b: string): DiffLine[] {
  const aLines = a.split("\n")
  const bLines = b.split("\n")
  const max = Math.max(aLines.length, bLines.length)
  const out: DiffLine[] = []
  for (let i = 0; i < max; i++) {
    const left = aLines[i]
    const right = bLines[i]
    if (left === right) {
      if (right !== undefined) out.push({ type: "same", text: right })
    } else {
      if (left !== undefined) out.push({ type: "remove", text: left })
      if (right !== undefined) out.push({ type: "add", text: right })
    }
  }
  return out
}

export function ConfigEditor() {
  const [saved, setSaved] = React.useState(SAVED_CONFIG)
  const [draft, setDraft] = React.useState(SAVED_CONFIG)
  const [lastAction, setLastAction] = React.useState<string | null>(null)

  const dirty = draft !== saved
  const jsonValid = React.useMemo(() => {
    try {
      JSON.parse(draft)
      return true
    } catch {
      return false
    }
  }, [draft])

  const diff = React.useMemo(() => computeDiff(saved, draft), [saved, draft])
  const changes = diff.filter((d) => d.type !== "same").length

  const handleSave = () => {
    if (!jsonValid) return
    setSaved(draft)
    setLastAction("Configuration applied to live cluster")
  }
  const handleRollback = () => {
    setDraft(saved)
    setLastAction("Reverted draft to last saved revision")
  }

  return (
    <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
      <Card className="flex flex-col">
        <CardHeader className="border-b border-border">
          <div className="flex items-center justify-between">
            <div className="flex flex-col gap-1">
              <CardTitle className="text-base">runtime.json</CardTitle>
              <CardDescription>Live server configuration draft</CardDescription>
            </div>
            {jsonValid ? (
              <StatusPill label="VALID JSON" tone="success" />
            ) : (
              <StatusPill label="INVALID JSON" tone="destructive" />
            )}
          </div>
        </CardHeader>
        <CardContent className="flex flex-1 flex-col gap-3 pt-4">
          <textarea
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            spellCheck={false}
            className={cn(
              "min-h-[420px] w-full flex-1 resize-none rounded-lg border border-border bg-muted/30 p-4 font-mono text-xs leading-relaxed text-foreground/90 outline-none",
              "focus-visible:ring-2 focus-visible:ring-ring/50",
              !jsonValid && "border-destructive/50",
            )}
            aria-label="Runtime configuration JSON editor"
          />
          <div className="flex flex-wrap items-center gap-2">
            <Button size="sm" onClick={handleSave} disabled={!dirty || !jsonValid}>
              <HugeiconsIcon icon={FloppyDiskIcon} strokeWidth={2} />
              Save & Apply
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={handleRollback}
              disabled={!dirty}
            >
              <HugeiconsIcon icon={ArrowTurnBackwardIcon} strokeWidth={2} />
              Rollback
            </Button>
            <span className="ml-auto font-mono text-xs text-muted-foreground">
              {dirty ? `${changes} pending change(s)` : "no pending changes"}
            </span>
          </div>
          {lastAction ? (
            <div className="flex items-center gap-2 rounded-lg border border-border bg-muted/30 px-3 py-2 text-xs text-muted-foreground">
              <HugeiconsIcon
                icon={jsonValid ? Tick02Icon : Alert02Icon}
                strokeWidth={2}
                className="size-4 text-success"
              />
              {lastAction}
            </div>
          ) : null}
        </CardContent>
      </Card>

      <Card className="flex flex-col">
        <CardHeader className="border-b border-border">
          <div className="flex items-center justify-between">
            <div className="flex flex-col gap-1">
              <CardTitle className="text-base">Diff Preview</CardTitle>
              <CardDescription>saved revision vs. current draft</CardDescription>
            </div>
            <span className="font-mono text-xs text-muted-foreground">
              rev #4821
            </span>
          </div>
        </CardHeader>
        <CardContent className="pt-4">
          <div className="overflow-hidden rounded-lg border border-border bg-muted/20 font-mono text-xs">
            {diff.length === 0 ? (
              <div className="p-4 text-muted-foreground">No differences.</div>
            ) : (
              diff.map((line, i) => (
                <div
                  key={i}
                  className={cn(
                    "flex items-start gap-2 px-3 py-0.5 leading-relaxed",
                    line.type === "add" && "bg-success/10 text-success",
                    line.type === "remove" && "bg-destructive/10 text-destructive",
                    line.type === "same" && "text-muted-foreground/70",
                  )}
                >
                  <span className="select-none opacity-60">
                    {line.type === "add" ? "+" : line.type === "remove" ? "-" : " "}
                  </span>
                  <span className="whitespace-pre-wrap break-all">{line.text}</span>
                </div>
              ))
            )}
          </div>
        </CardContent>
      </Card>
    </div>
  )
}
