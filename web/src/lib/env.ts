export const env = {
  apiBase: import.meta.env.VITE_API_BASE ?? "/api",
  wsBase: import.meta.env.VITE_WS_BASE ?? `${location.protocol === "https:" ? "wss" : "ws"}://${location.host}/api/ws`,
}