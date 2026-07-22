import { useEffect } from "react"

import { useWebSocket, } from "@/hooks/useWebSocket"
import { useWebSocketClient } from "@/providers/WebSocketProvider"

export function useAdminSubscription(enabled = true) {
  const client = useWebSocketClient()
  const connection = useWebSocket()

  useEffect(() => {
    if (!enabled) {
      return
    }

    console.debug("acquire admin subscription")
    const release = client.acquireAdmin()

    return () => {
      console.debug("release admin subscription")
      release()
    }
  }, [client, enabled])

  return connection
}
