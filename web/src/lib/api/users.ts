import type { ApiClient } from "@/lib/api/client"
import type {
  AdminUserDetailResponse,
  AdminUsersResponse,
  ApiSuccess,
  BanRoomUserRequest,
  BanUserRequest,
  MoveUserRequest,
} from "@/lib/api/types"

export function createUsersApi(client: ApiClient) {
  return {
    getAdminUsers() {
      return client.get<AdminUsersResponse>("/admin/users")
    },
    getAdminUser(id: number) {
      return client.get<AdminUserDetailResponse>(`/admin/users/${id}`)
    },
    disconnectUser(id: number) {
      return client.post<ApiSuccess>(`/admin/users/${id}/disconnect`)
    },
    moveUser(id: number, request: MoveUserRequest) {
      return client.post<ApiSuccess>(`/admin/users/${id}/move`, {
        roomId: request.roomId,
        ...(request.monitor === undefined ? {} : { monitor: request.monitor }),
      })
    },
    banUser(request: BanUserRequest) {
      return client.post<ApiSuccess>("/admin/ban/user", request)
    },
    banRoomUser(request: BanRoomUserRequest) {
      return client.post<ApiSuccess>("/admin/ban/room", request)
    },
  }
}
