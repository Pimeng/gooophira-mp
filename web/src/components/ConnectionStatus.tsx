import { useWebSocket } from "@/hooks/useWebSocket"

const statusLabels = {
  connecting: "连接中",
  open: "已连接",
  reconnecting: "重连中",
  error: "连接错误",
  closed: "未连接",
} as const

const statusColors = {
  connecting: "bg-amber-500",
  open: "bg-emerald-500",
  reconnecting: "bg-amber-500",
  error: "bg-red-500",
  closed: "bg-slate-400",
} as const

export function ConnectionStatus() {
  const { data: state } = useWebSocket()

  return (
    <div className="flex items-center gap-2 text-xs text-slate-500" aria-live="polite">
      <span className={`h-2 w-2 rounded-full ${statusColors[state]}`} aria-hidden="true" />
      <span>{statusLabels[state]}</span>
    </div>
  )
}
