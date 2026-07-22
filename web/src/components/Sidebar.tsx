import { NavLink } from "react-router-dom"

const navigation = [
  { href: "/dashboard", label: "仪表盘" },
  { href: "/rooms", label: "房间" },
  { href: "/players", label: "玩家" },
  { href: "/bans", label: "封禁" },
  { href: "/system", label: "系统状态" },
  { href: "/settings", label: "系统设置" },
] as const

export function Sidebar() {
  return (
    <aside className="flex w-full shrink-0 flex-col border-b border-slate-200 bg-slate-950 text-slate-300 md:min-h-screen md:w-64 md:border-b-0 md:border-r md:border-slate-800">
      <div className="flex items-center justify-between px-5 py-5 md:block">
        <div>
          <p className="text-xs font-semibold uppercase tracking-[0.28em] text-cyan-400">Gooophira</p>
          <h1 className="mt-2 text-lg font-semibold tracking-tight text-white">管理控制台</h1>
        </div>
      </div>
      <nav className="flex gap-1 overflow-x-auto px-3 pb-3 md:block md:space-y-1 md:px-3 md:pb-0" aria-label="主导航">
        {navigation.map((item) => (
          <NavLink
            key={item.href}
            to={item.href}
            className={({ isActive }) =>
              `block whitespace-nowrap rounded-md px-3 py-2 text-sm transition-colors ${
                isActive
                  ? "bg-cyan-400/15 font-medium text-cyan-300"
                  : "text-slate-400 hover:bg-white/5 hover:text-white"
              }`
            }
          >
            {item.label}
          </NavLink>
        ))}
      </nav>
    </aside>
  )
}
