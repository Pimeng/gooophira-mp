const API_BASE = "/api"

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = "ApiError"
  }
}

function adminToken() {
  if (typeof window === "undefined") return ""
  return localStorage.getItem("phira_mp_admin_token") || localStorage.getItem("gooophira_admin_token") || ""
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set("Accept", "application/json")
  const token = adminToken()
  if (token) headers.set("X-Admin-Token", token)
  const response = await fetch(`${API_BASE}${path}`, { ...init, headers, cache: "no-store" })
  if (!response.ok) {
    if (response.status === 401 || response.status === 403) window.dispatchEvent(new CustomEvent("admin-auth-required"))
    if (response.status === 429) window.dispatchEvent(new CustomEvent("api-rate-limited"))
    let message = response.statusText
    try { message = (await response.json()).message || message } catch {}
    throw new ApiError(response.status, message)
  }
  return response.status === 204 ? (undefined as T) : response.json() as Promise<T>
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) }),
  delete: <T>(path: string) => request<T>(path, { method: "DELETE" }),
}

export function getAdminToken() { return adminToken() }

export type MetricsResponse = {
  rooms?: { count?: number; totalPlayers?: number }
  network?: { messagesPerSecond?: number; totalMessages?: number; totalErrors?: number; errorRate?: number }
  agent?: { workers?: Array<{ name: string; role?: string; load?: number; status?: string }> }
  business?: { activeRooms?: number; onlineUsers?: number }
}
