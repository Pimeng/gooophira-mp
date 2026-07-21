import { Message01Icon } from "@hugeicons/core-free-icons"
import { PlaceholderPage } from "@/components/dashboard/placeholder-page"

export default function FeishuPage() {
  return (
    <PlaceholderPage
      title="Feishu Integration"
      description="Route alerts and operations events to Feishu channels."
      icon={Message01Icon}
      endpoints={[
        { method: "POST", path: "/feishu/webhook", desc: "Register an incoming webhook" },
        { method: "POST", path: "/feishu/alert", desc: "Dispatch an alert message" },
        { method: "GET", path: "/feishu/channels", desc: "List bound channels" },
        { method: "PUT", path: "/feishu/config", desc: "Update severity routing" },
      ]}
    />
  )
}
