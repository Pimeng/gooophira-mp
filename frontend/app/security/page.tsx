import { ServerStack01Icon } from "@hugeicons/core-free-icons"
import { PlaceholderPage } from "@/components/dashboard/placeholder-page"

export default function SecurityPage() {
  return (
    <PlaceholderPage
      title="Security Center"
      description="Access control, audit logs, and threat monitoring."
      icon={ServerStack01Icon}
      endpoints={[
        { method: "GET", path: "/security/audit", desc: "Query the audit log" },
        { method: "GET", path: "/security/tokens", desc: "List active API tokens" },
        { method: "POST", path: "/security/rotate", desc: "Rotate signing keys" },
        { method: "GET", path: "/security/threats", desc: "Recent anomaly detections" },
      ]}
    />
  )
}
