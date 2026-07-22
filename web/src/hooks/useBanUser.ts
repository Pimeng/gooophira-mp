import { useMutation, useQueryClient } from "@tanstack/react-query"

import type { BanUserRequest } from "@/lib/api/types"
import { createUsersApi } from "@/lib/api/users"
import { useApiClient } from "@/providers/AppProviders"

export function useBanUser() {
  const client = useApiClient()
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (request: BanUserRequest) => createUsersApi(client).banUser(request),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: ["admin", "users"] }),
  })
}
