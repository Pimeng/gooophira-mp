import { cn } from "@/lib/utils"

export function PageHeader({
  title,
  description,
  actions,
  className,
}: {
  title: string
  description?: string
  actions?: React.ReactNode
  className?: string
}) {
  return (
    <div
      className={cn(
        "flex flex-col gap-3 border-b border-border pb-5 sm:flex-row sm:items-center sm:justify-between",
        className,
      )}
    >
      <div className="flex flex-col gap-1">
        <h1 className="text-balance text-xl font-semibold tracking-tight">
          {title}
        </h1>
        {description ? (
          <p className="text-pretty text-sm text-muted-foreground">
            {description}
          </p>
        ) : null}
      </div>
      {actions ? <div className="flex items-center gap-2">{actions}</div> : null}
    </div>
  )
}

export function EndpointBadge({
  method,
  path,
  className,
}: {
  method?: "GET" | "POST" | "PUT" | "DELETE" | "WS"
  path: string
  className?: string
}) {
  const methodTone: Record<string, string> = {
    GET: "text-success",
    POST: "text-chart-2",
    PUT: "text-warning",
    DELETE: "text-destructive",
    WS: "text-chart-2",
  }
  return (
    <span
      className={cn(
        "inline-flex items-center gap-2 rounded-md border border-border bg-muted/40 px-2 py-0.5 font-mono text-xs",
        className,
      )}
    >
      {method ? (
        <span className={cn("font-semibold", methodTone[method])}>{method}</span>
      ) : null}
      <span className="text-foreground/90">{path}</span>
    </span>
  )
}
