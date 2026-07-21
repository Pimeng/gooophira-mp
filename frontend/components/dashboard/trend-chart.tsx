"use client"

import {
  Area,
  AreaChart,
  CartesianGrid,
  XAxis,
  YAxis,
} from "recharts"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart"

const config = {
  players: { label: "Online players", color: "var(--chart-1)" },
  messages: { label: "Messages", color: "var(--chart-2)" },
} satisfies ChartConfig

export function TrendChart({ data }: { data: { t: string; players: number; messages: number }[] }) {
  return (
    <Card className="h-full">
      <CardHeader>
        <CardTitle className="text-base">24h Activity Trend</CardTitle>
        <CardDescription>
          Concurrent players and message throughput over the last day
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ChartContainer config={config} className="h-[260px] w-full">
          <AreaChart data={data} margin={{ left: -12, right: 8, top: 8 }}>
            <defs>
              <linearGradient id="fillPlayers" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--color-players)" stopOpacity={0.35} />
                <stop offset="100%" stopColor="var(--color-players)" stopOpacity={0} />
              </linearGradient>
              <linearGradient id="fillMessages" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor="var(--color-messages)" stopOpacity={0.25} />
                <stop offset="100%" stopColor="var(--color-messages)" stopOpacity={0} />
              </linearGradient>
            </defs>
            <CartesianGrid vertical={false} stroke="var(--border)" strokeDasharray="3 3" />
            <XAxis
              dataKey="t"
              tickLine={false}
              axisLine={false}
              tickMargin={8}
              minTickGap={24}
              className="font-mono text-[0.65rem]"
            />
            <YAxis
              tickLine={false}
              axisLine={false}
              width={40}
              className="font-mono text-[0.65rem]"
            />
            <ChartTooltip content={<ChartTooltipContent indicator="line" />} />
            <Area
              dataKey="messages"
              type="monotone"
              stroke="var(--color-messages)"
              fill="url(#fillMessages)"
              strokeWidth={1.5}
            />
            <Area
              dataKey="players"
              type="monotone"
              stroke="var(--color-players)"
              fill="url(#fillPlayers)"
              strokeWidth={1.5}
            />
          </AreaChart>
        </ChartContainer>
      </CardContent>
    </Card>
  )
}
