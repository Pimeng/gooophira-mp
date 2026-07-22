import { useWebSocketState } from "@/providers/WebSocketProvider"

export function useWebSocket() {
  const data = useWebSocketState()
  return {
    data,
    loading: data !== "open",
    error: data === "error" ? new Error("WebSocket connection failed") : null,
  }
}
