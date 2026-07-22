import type { ApiClient } from "@/lib/api/client"
import type { AdminRoom, AdminRoomsResponse, ApiSuccess } from "@/lib/api/types"

export function createRoomsApi(client: ApiClient) {
  return {
    getAdminRooms() {
      return client.get<AdminRoomsResponse>("/admin/rooms")
    },
    async getAdminRoom(roomId: string): Promise<AdminRoom | undefined> {
      const response = await client.get<AdminRoomsResponse>("/admin/rooms")
      return response.rooms.find((room) => room.roomid === roomId)
    },
    disbandRoom(roomId: string) {
      return client.post<ApiSuccess>("/admin/disband", { roomid: roomId })
    },
    broadcastMessage(message: string) {
      return client.post<{ ok: true; rooms: number }>("/admin/broadcast", { message })
    },
  }
}
