import { useQuery } from "@tanstack/react-query"

import { createConsoleApi } from "@/lib/api/console"
import type { ConsoleLogsResponse } from "@/lib/api/types"
import { useApiClient } from "@/providers/AppProviders"

export function useConsoleLogs(limit?: number) {
  const client = useApiClient()
  const query = useQuery<ConsoleLogsResponse>({
    queryKey: ["console", "logs"],
    queryFn: () => createConsoleApi(client).getConsoleLogs(limit),
  })
  return { ...query, loading: query.isLoading, error: query.error, data: query.data }
}
