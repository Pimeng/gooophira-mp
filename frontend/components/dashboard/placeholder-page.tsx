import { HugeiconsIcon } from "@hugeicons/react"
import { type DashboardSquare01Icon } from "@hugeicons/core-free-icons"
import { PageHeader, EndpointBadge } from "@/components/dashboard/page-header"
import { Card, CardContent } from "@/components/ui/card"

export function PlaceholderPage({
  title,
  description,
  icon,
  endpoints,
}: {
  title: string
  description: string
  icon: typeof DashboardSquare01Icon
  endpoints: { method: "GET" | "POST" | "PUT" | "DELETE" | "WS"; path: string; desc: string }[]
}) {
  return (
    <div className="flex flex-col gap-6 p-4 md:p-6 lg:p-8">
      <PageHeader title={title} description={description} />
      <Card>
        <CardContent className="flex flex-col items-center gap-4 py-14 text-center">
          <span className="flex size-14 items-center justify-center rounded-2xl bg-muted text-muted-foreground">
            <HugeiconsIcon icon={icon} strokeWidth={2} className="size-7" />
          </span>
          <div className="flex max-w-md flex-col gap-1">
            <h2 className="text-base font-medium">Module scaffolding ready</h2>
            <p className="text-pretty text-sm text-muted-foreground">
              This section is wired into the console shell. Available API
              endpoints for this module are listed below.
            </p>
          </div>
        </CardContent>
      </Card>
      <Card>
        <CardContent className="flex flex-col divide-y divide-border p-0">
          {endpoints.map((e) => (
            <div
              key={e.path}
              className="flex flex-col gap-2 px-5 py-3.5 sm:flex-row sm:items-center sm:justify-between"
            >
              <EndpointBadge method={e.method} path={e.path} />
              <span className="text-sm text-muted-foreground">{e.desc}</span>
            </div>
          ))}
        </CardContent>
      </Card>
    </div>
  )
}
