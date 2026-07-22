import { useMutation, useQueryClient } from "@tanstack/react-query"

import type { BanRoomUserRequest } from "@/lib/api/types"
import { createUsersApi } from "@/lib/api/users"
import { useApiClient } from "@/providers/AppProviders"

export function useBanRoomUser() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (request: BanRoomUserRequest) => createUsersApi(client).banRoomUser(request),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["admin", "users"] }),
  })
}
