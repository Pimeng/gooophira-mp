import {
  DashboardSquare01Icon,
  GlobalIcon,
  SecurityCheckIcon,
  Analytics01Icon,
  Settings01Icon,
  ServerStack01Icon,
  Message01Icon,
} from "@hugeicons/core-free-icons"

export type NavItem = {
  label: string
  href: string
  icon: typeof DashboardSquare01Icon
  badge?: string
}

export const navItems: NavItem[] = [
  { label: "Dashboard", href: "/", icon: DashboardSquare01Icon },
  { label: "Public Services", href: "/public-services", icon: GlobalIcon },
  { label: "Admin Center", href: "/admin-center", icon: SecurityCheckIcon },
  {
    label: "Realtime Monitoring",
    href: "/realtime",
    icon: Analytics01Icon,
    badge: "LIVE",
  },
  {
    label: "Feishu Integration",
    href: "/feishu",
    icon: Message01Icon,
  },
  { label: "System Config", href: "/config", icon: Settings01Icon },
  { label: "Security Center", href: "/security", icon: ServerStack01Icon },
]
