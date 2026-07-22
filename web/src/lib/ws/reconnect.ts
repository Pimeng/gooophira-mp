export interface ReconnectOptions {
  initialDelayMs?: number
  maxDelayMs?: number
  jitterRatio?: number
  random?: () => number
}

export class ReconnectController {
  private readonly initialDelayMs: number
  private readonly maxDelayMs: number
  private readonly jitterRatio: number
  private readonly random: () => number
  private attempt = 0
  private timer: ReturnType<typeof setTimeout> | undefined

  public constructor(options: ReconnectOptions = {}) {
    this.initialDelayMs = options.initialDelayMs ?? 1_000
    this.maxDelayMs = options.maxDelayMs ?? 30_000
    this.jitterRatio = options.jitterRatio ?? 0.25
    this.random = options.random ?? Math.random
  }

  public schedule(callback: () => void): void {
    this.cancel()
    const baseDelay = Math.min(
      this.maxDelayMs,
      this.initialDelayMs * 2 ** this.attempt,
    )
    const jitter = baseDelay * this.jitterRatio * this.random()
    this.attempt += 1
    this.timer = setTimeout(callback, baseDelay + jitter)
  }

  public reset(): void {
    this.attempt = 0
  }

  public cancel(): void {
    if (this.timer !== undefined) {
      clearTimeout(this.timer)
      this.timer = undefined
    }
  }
}
