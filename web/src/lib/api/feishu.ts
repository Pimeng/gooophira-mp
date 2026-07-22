import type { ApiClient } from "@/lib/api/client"
import type {
  CreateFeishuRegistrationRequest,
  FeishuRegistrationTask,
} from "@/lib/api/types"

export function createFeishuApi(client: ApiClient) {
  return {
    createRegistration(request: CreateFeishuRegistrationRequest) {
      return client.post<FeishuRegistrationTask>("/admin/feishu/app-registration", request)
    },
    getRegistration(taskId: string) {
      return client.get<FeishuRegistrationTask>(
        `/admin/feishu/app-registration/${encodeURIComponent(taskId)}`,
      )
    },
    cancelRegistration(taskId: string) {
      return client.delete<FeishuRegistrationTask>(
        `/admin/feishu/app-registration/${encodeURIComponent(taskId)}`,
      )
    },
  }
}
