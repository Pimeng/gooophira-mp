import { GlobalIcon } from "@hugeicons/core-free-icons"
import { PlaceholderPage } from "@/components/dashboard/placeholder-page"

export default function PublicServicesPage() {
  return (
    <PlaceholderPage
      title="Public Services"
      description="Public-facing endpoints exposed to game clients."
      icon={GlobalIcon}
      endpoints={[
        { method: "GET", path: "/v1/servers", desc: "List available game regions" },
        { method: "POST", path: "/v1/matchmake", desc: "Request a matchmaking ticket" },
        { method: "GET", path: "/v1/leaderboard", desc: "Fetch global rankings" },
        { method: "WS", path: "/ws/room_update", desc: "Client room event stream" },
      ]}
    />
  )
}
