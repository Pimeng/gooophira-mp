import { SecurityCheckIcon } from "@hugeicons/core-free-icons"
import { PlaceholderPage } from "@/components/dashboard/placeholder-page"

export default function AdminCenterPage() {
  return (
    <PlaceholderPage
      title="Admin Center"
      description="Privileged operations for the game server cluster."
      icon={SecurityCheckIcon}
      endpoints={[
        { method: "GET", path: "/admin/metrics", desc: "Cluster-wide metrics snapshot" },
        { method: "GET", path: "/admin/rooms", desc: "Inspect live room sessions" },
        { method: "POST", path: "/admin/ban", desc: "Issue a player ban" },
        { method: "DELETE", path: "/admin/session/:id", desc: "Force-kick a session" },
      ]}
    />
  )
}
