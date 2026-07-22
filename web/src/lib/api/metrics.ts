import type { ApiClient } from "@/lib/api/client"
import type { AdminMetricsResponse } from "@/lib/api/types"

export function createMetricsApi(client: ApiClient) {
  return {
    getAdminMetrics(options?: { history?: boolean }) {
      const query = options?.history ? "?history=1" : ""
      return client.get<AdminMetricsResponse>(`/admin/metrics${query}`)
    },
  }
}
