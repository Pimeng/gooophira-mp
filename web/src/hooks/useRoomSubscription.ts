import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect } from "react"

import { useWebSocket } from "@/hooks/useWebSocket"
import type { RoomUpdateData } from "@/lib/ws/protocol"
import { useWebSocketClient } from "@/providers/WebSocketProvider"

export function useRoomSubscription(roomId: string | undefined, enabled = true) {
  const client = useWebSocketClient()
  const connection = useWebSocket()
  const queryClient = useQueryClient()
  const query = useQuery<RoomUpdateData | undefined>({
    queryKey: ["room", roomId],
    queryFn: () => queryClient.getQueryData<RoomUpdateData>(["room", roomId]),
    enabled: false,
  })

  useEffect(() => {
    if (!enabled || roomId === undefined) return
    return client.acquireRoom(roomId)
  }, [client, enabled, roomId])

  return { ...connection, data: query.data, connection: connection.data }
}
