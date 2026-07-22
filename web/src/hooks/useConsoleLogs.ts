import { useQuery } from "@tanstack/react-query"

import { useConsoleSubscription } from "@/hooks/useConsoleSubscription"
import { createConsoleApi } from "@/lib/api/console"
import type { ConsoleLogsResponse } from "@/lib/api/types"
import { useApiClient } from "@/providers/AppProviders"

export function useConsoleLogs(limit?: number) {
  const client = useApiClient()
  const subscription = useConsoleSubscription()
  const query = useQuery<ConsoleLogsResponse>({
    queryKey: ["console", "logs"],
    queryFn: () => createConsoleApi(client).getConsoleLogs(limit),
  })
  return {
    ...query,
    loading: query.isLoading || subscription.loading,
    error: query.error ?? subscription.error,
    data: query.data,
  }
}
