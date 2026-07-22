import { useQuery } from "@tanstack/react-query"

import { useAdminSubscription } from "@/hooks/useAdminSubscription"
import type { AdminUsersResponse } from "@/lib/api/types"
import { createUsersApi } from "@/lib/api/users"
import { useApiClient } from "@/providers/AppProviders"

export function useAdminUsers() {
  const client = useApiClient()
  const subscription = useAdminSubscription()
  const query = useQuery<AdminUsersResponse>({
    queryKey: ["admin", "users"],
    queryFn: () => createUsersApi(client).getAdminUsers(),
  })

  return {
    ...query,
    loading: query.isLoading || subscription.loading,
    error: query.error ?? subscription.error,
    data: query.data,
  }
}
