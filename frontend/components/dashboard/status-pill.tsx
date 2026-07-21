import { cn } from "@/lib/utils"

type Tone = "success" | "warning" | "destructive"

const toneMap: Record<Tone, { dot: string; text: string; bg: string }> = {
  success: {
    dot: "bg-success",
    text: "text-success",
    bg: "bg-success/10 border-success/20",
  },
  warning: {
    dot: "bg-warning",
    text: "text-warning",
    bg: "bg-warning/10 border-warning/20",
  },
  destructive: {
    dot: "bg-destructive",
    text: "text-destructive",
    bg: "bg-destructive/10 border-destructive/20",
  },
}

export function StatusPill({
  label,
  tone = "success",
  pulse = false,
  className,
}: {
  label: string
  tone?: Tone
  pulse?: boolean
  className?: string
}) {
  const t = toneMap[tone]
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 font-mono text-[0.7rem] font-medium",
        t.bg,
        t.text,
        className,
      )}
    >
      <span className="relative flex size-2">
        {pulse ? (
          <span
            className={cn(
              "absolute inline-flex size-full animate-ping rounded-full opacity-75",
              t.dot,
            )}
          />
        ) : null}
        <span className={cn("relative inline-flex size-2 rounded-full", t.dot)} />
      </span>
      {label}
    </span>
  )
}
