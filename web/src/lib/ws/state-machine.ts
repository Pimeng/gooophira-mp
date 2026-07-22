export type WebSocketState =
  | "connecting"
  | "open"
  | "reconnecting"
  | "closed"
  | "error"

export type WebSocketStateListener = (state: WebSocketState) => void

const transitions: Record<WebSocketState, readonly WebSocketState[]> = {
  connecting: ["open", "error", "closed"],
  open: ["reconnecting", "error", "closed"],
  reconnecting: ["connecting", "closed", "error"],
  closed: ["connecting"],
  error: ["reconnecting", "connecting", "closed"],
}

export class WebSocketStateMachine {
  private current: WebSocketState = "closed"
  private readonly listeners = new Set<WebSocketStateListener>()

  public getState(): WebSocketState {
    return this.current
  }

  public subscribe(listener: WebSocketStateListener): () => void {
    this.listeners.add(listener)
    listener(this.current)
    return () => this.listeners.delete(listener)
  }

  public transition(next: WebSocketState): void {
    if (next === this.current) {
      return
    }

    if (!transitions[this.current].includes(next)) {
      return
    }

    this.current = next
    for (const listener of this.listeners) {
      listener(next)
    }
  }

  public close(): void {
    this.transition("closed")
  }
}
