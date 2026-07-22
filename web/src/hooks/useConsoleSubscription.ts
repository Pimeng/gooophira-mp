import { useEffect } from "react"

import { useWebSocket } from "@/hooks/useWebSocket"
import { useWebSocketClient } from "@/providers/WebSocketProvider"

export function useConsoleSubscription(enabled = true) {
  const client = useWebSocketClient()
  const connection = useWebSocket()

  useEffect(() => {
    if (!enabled) return
    return client.acquireConsole()
  }, [client, enabled])

  return connection
}
