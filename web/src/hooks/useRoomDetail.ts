import { useQuery } from "@tanstack/react-query"

import { useRoomSubscription } from "@/hooks/useRoomSubscription"
import { createRoomsApi } from "@/lib/api/rooms"
import type { AdminRoom } from "@/lib/api/types"
import type { RoomUpdateData } from "@/lib/ws/protocol"
import { useApiClient } from "@/providers/AppProviders"

type RoomDetail = AdminRoom | RoomUpdateData

export function useRoomDetail(roomId: string | undefined, enabled = true) {
  const client = useApiClient()
  const subscription = useRoomSubscription(roomId, enabled)
  const query = useQuery<RoomDetail | undefined>({
    queryKey: ["room", roomId],
    queryFn: () => (roomId === undefined ? undefined : createRoomsApi(client).getAdminRoom(roomId)),
    enabled: enabled && roomId !== undefined,
  })

  return {
    ...query,
    loading: query.isLoading || subscription.loading,
    error: query.error ?? subscription.error,
    data: query.data,
  }
}
