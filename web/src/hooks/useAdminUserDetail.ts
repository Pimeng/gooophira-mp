import { useQuery } from "@tanstack/react-query"

import type { AdminUserDetailResponse } from "@/lib/api/types"
import { createUsersApi } from "@/lib/api/users"
import { useApiClient } from "@/providers/AppProviders"

export function useAdminUserDetail(userId: number | undefined) {
  const client = useApiClient()
  const query = useQuery<AdminUserDetailResponse>({
    queryKey: ["admin", "users", userId],
    queryFn: () => createUsersApi(client).getAdminUser(userId as number),
    enabled: userId !== undefined,
  })

  return {
    ...query,
    loading: query.isLoading,
    error: query.error,
    data: query.data,
  }
}
