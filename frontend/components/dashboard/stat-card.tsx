import { HugeiconsIcon } from "@hugeicons/react"
import {
  ArrowUpRight01Icon,
  ArrowDown01Icon,
} from "@hugeicons/core-free-icons"
import { Card, CardContent } from "@/components/ui/card"
import { cn } from "@/lib/utils"

export function StatCard({
  label,
  value,
  unit,
  delta,
  deltaDir = "up",
  positiveIsGood = true,
  icon,
  spark,
}: {
  label: string
  value: string
  unit?: string
  delta?: string
  deltaDir?: "up" | "down"
  positiveIsGood?: boolean
  icon: typeof ArrowUpRight01Icon
  spark?: React.ReactNode
}) {
  const good = deltaDir === "up" ? positiveIsGood : !positiveIsGood
  return (
    <Card className="gap-0 overflow-hidden">
      <CardContent className="flex flex-col gap-3 py-5">
        <div className="flex items-center justify-between">
          <span className="text-sm text-muted-foreground">{label}</span>
          <span className="flex size-8 items-center justify-center rounded-lg bg-muted text-muted-foreground">
            <HugeiconsIcon icon={icon} strokeWidth={2} className="size-4" />
          </span>
        </div>
        <div className="flex items-end gap-1.5">
          <span className="font-mono text-2xl font-semibold tracking-tight tabular-nums">
            {value}
          </span>
          {unit ? (
            <span className="pb-0.5 text-xs text-muted-foreground">{unit}</span>
          ) : null}
        </div>
        <div className="flex items-center justify-between">
          {delta ? (
            <span
              className={cn(
                "inline-flex items-center gap-1 font-mono text-xs font-medium",
                good ? "text-success" : "text-destructive",
              )}
            >
              <HugeiconsIcon
                icon={deltaDir === "up" ? ArrowUpRight01Icon : ArrowDown01Icon}
                strokeWidth={2}
                className="size-3.5"
              />
              {delta}
            </span>
          ) : (
            <span />
          )}
          {spark ? <div className="h-6 w-20">{spark}</div> : null}
        </div>
      </CardContent>
    </Card>
  )
}
