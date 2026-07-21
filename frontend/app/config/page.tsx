import { PageHeader, EndpointBadge } from "@/components/dashboard/page-header"
import { ConfigEditor } from "@/components/config/config-editor"

export default function ConfigPage() {
  return (
    <div className="flex flex-col gap-6 p-4 md:p-6 lg:p-8">
      <PageHeader
        title="System Config"
        description="Edit and hot-reload the runtime configuration for the game server cluster."
        actions={<EndpointBadge method="PUT" path="/admin/config/runtime" />}
      />
      <ConfigEditor />
    </div>
  )
}
