import type { ApiClient } from "@/lib/api/client"
import type {
  ApiSuccess,
  RuntimeConfig,
  RuntimeConfigResponse,
  RuntimeConfigUpdateResponse,
} from "@/lib/api/types"

interface ToggleConfigResponse {
  ok: true
  enabled: boolean
}

export function createConfigApi(client: ApiClient) {
  return {
    getRuntimeConfig() {
      return client.get<RuntimeConfigResponse>("/admin/runtime-config")
    },
    updateRuntimeConfig(patch: RuntimeConfig) {
      return client.post<RuntimeConfigUpdateResponse>("/admin/runtime-config", patch)
    },
    rollbackRuntimeConfig() {
      return client.post<RuntimeConfigUpdateResponse>("/admin/runtime-config/rollback")
    },
    getReplayConfig() {
      return client.get<ToggleConfigResponse>("/admin/replay/config")
    },
    setReplayConfig(enabled: boolean) {
      return client.post<ToggleConfigResponse>("/admin/replay/config", { enabled })
    },
    getRoomCreationConfig() {
      return client.get<ToggleConfigResponse>("/admin/room-creation/config")
    },
    setRoomCreationConfig(enabled: boolean) {
      return client.post<ToggleConfigResponse>("/admin/room-creation/config", { enabled })
    },
  }
}

export type ConfigApi = ReturnType<typeof createConfigApi>

export type ToggleConfigSuccess = ApiSuccess & { enabled: boolean }
