import { ConnectionStatus } from "@/components/ConnectionStatus"

interface HeaderProps {
  title?: string
}

export function Header({ title = "管理控制台" }: HeaderProps) {
  return (
    <header className="flex min-h-16 items-center justify-between gap-4 border-b border-slate-200 bg-white px-5 py-4 md:px-8">
      <div>
        <p className="text-xs font-medium uppercase tracking-[0.2em] text-slate-400">Admin workspace</p>
        <h2 className="mt-1 text-xl font-semibold tracking-tight text-slate-900">{title}</h2>
      </div>
      <ConnectionStatus />
    </header>
  )
}
