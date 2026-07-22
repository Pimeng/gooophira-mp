import type { ApiClient } from "@/lib/api/client"
import type {
  ApiSuccess,
  ContestConfigRequest,
  ContestStartRequest,
  ContestWhitelistRequest,
} from "@/lib/api/types"

function roomPath(roomId: string, action: string) {
  return `/admin/contest/rooms/${encodeURIComponent(roomId)}/${action}`
}

export function createContestApi(client: ApiClient) {
  return {
    configureContest(roomId: string, request: ContestConfigRequest) {
      return client.post<ApiSuccess>(roomPath(roomId, "config"), request)
    },
    setContestWhitelist(roomId: string, request: ContestWhitelistRequest) {
      return client.post<ApiSuccess>(roomPath(roomId, "whitelist"), request)
    },
    startContest(roomId: string, request?: ContestStartRequest) {
      return client.post<ApiSuccess>(roomPath(roomId, "start"), request)
    },
  }
}
