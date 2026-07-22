import type { ApiClient } from "@/lib/api/client"
import type { AdminRoomsResponse, ApiSuccess } from "@/lib/api/types"

export function createRoomsApi(client: ApiClient) {
  return {
    getAdminRooms() {
      return client.get<AdminRoomsResponse>("/admin/rooms")
    },
    disbandRoom(roomId: string) {
      return client.post<ApiSuccess>("/admin/disband", { roomid: roomId })
    },
    broadcastMessage(message: string) {
      return client.post<{ ok: true; rooms: number }>("/admin/broadcast", { message })
    },
  }
}
