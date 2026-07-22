import { useQuery } from "@tanstack/react-query"

import { createMetricsApi } from "@/lib/api/metrics"
import type { AdminMetricsResponse } from "@/lib/api/types"
import { useApiClient } from "@/providers/AppProviders"

export function useAdminMetrics(history = false) {
  const client = useApiClient()
  const query = useQuery<AdminMetricsResponse>({
    queryKey: ["admin", "metrics", { history }],
    queryFn: () => createMetricsApi(client).getAdminMetrics({ history }),
  })
  return { ...query, loading: query.isLoading, error: query.error, data: query.data }
}
