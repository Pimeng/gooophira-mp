import { useQuery } from "@tanstack/react-query"

import { createRoomsApi } from "@/lib/api/rooms"
import type { AdminRoomsResponse } from "@/lib/api/types"
import { useApiClient } from "@/providers/AppProviders"

export function useAdminRooms() {
  const client = useApiClient()
  const query = useQuery<AdminRoomsResponse>({
    queryKey: ["admin", "rooms"],
    queryFn: () => createRoomsApi(client).getAdminRooms(),
  })
  return { ...query, loading: query.isLoading, error: query.error, data: query.data }
}
