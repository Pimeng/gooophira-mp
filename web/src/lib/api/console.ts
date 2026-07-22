import type { ApiClient } from "@/lib/api/client"
import type {
  ConsoleCommandRequest,
  ConsoleCommandResponse,
  ConsoleLogsResponse,
} from "@/lib/api/types"

export function createConsoleApi(client: ApiClient) {
  return {
    getConsoleLogs(limit?: number) {
      const query = limit === undefined ? "" : `?limit=${encodeURIComponent(limit)}`
      return client.get<ConsoleLogsResponse>(`/admin/console/logs${query}`)
    },
    executeConsoleCommand(request: ConsoleCommandRequest) {
      return client.post<ConsoleCommandResponse>("/admin/console/command", request)
    },
  }
}
