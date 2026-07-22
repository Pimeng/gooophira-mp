import type { QueryClient } from "@tanstack/react-query"
import { useQueryClient } from "@tanstack/react-query"
import type { ReactNode } from "react"
import { createContext, useContext, useEffect, useRef, useSyncExternalStore } from "react"

import type { AdminTokenProvider } from "@/lib/api/token-provider"
import { createMemoryTokenProvider } from "@/lib/api/token-provider"
import type { AdminRoomsResponse, ConsoleLogsResponse } from "@/lib/api/types"
import { createWebSocketClient, type WebSocketClient } from "@/lib/ws/client"
import type { RoomUpdateData, WsServerEvent } from "@/lib/ws/protocol"
import type { WebSocketState } from "@/lib/ws/state-machine"

const adminRoomsKey = ["admin", "rooms"] as const
const consoleLogsKey = ["console", "logs"] as const

interface WebSocketContextValue {
  client: WebSocketClient
}

const WebSocketContext = createContext<WebSocketContextValue | undefined>(undefined)

export interface WebSocketProviderProps {
  children: ReactNode
  tokenProvider?: AdminTokenProvider
  wsBase?: string
}

function updateCache(queryClient: QueryClient, event: WsServerEvent): void {
  if (event.type === "admin_update") {
    // The backend builds this list from all current rooms, so absence means removal.
    queryClient.setQueryData<AdminRoomsResponse>(adminRoomsKey, () => ({
      ok: true,
      rooms: event.data.changes.rooms,
    }))
  }

  if (event.type === "room_update") {
    queryClient.setQueryData<RoomUpdateData>(["room", event.data.roomid], event.data)
  }

  if (event.type === "console_subscribed") {
    queryClient.setQueriesData<ConsoleLogsResponse>(
      { queryKey: consoleLogsKey },
      { ok: true, lines: event.data.lines },
    )
  }

  if (event.type === "console_log") {
    queryClient.setQueriesData<ConsoleLogsResponse>({ queryKey: consoleLogsKey }, (current) => ({
      ok: true,
      lines: [...(current?.lines ?? []), event.data].slice(-500),
    }))
  }
}

export function WebSocketProvider({ children, tokenProvider, wsBase }: WebSocketProviderProps) {
  const queryClient = useQueryClient()
  const clientRef = useRef<WebSocketClient | null>(null)

  if (clientRef.current === null) {
    const provider = tokenProvider ?? createMemoryTokenProvider()
    clientRef.current = createWebSocketClient({
      wsBase,
      tokenProvider: provider,
      onEvent: (event) => updateCache(queryClient, event),
    })
  }

  const client = clientRef.current

  useEffect(() => {
    client.connect()
    return () => client.destroy()
  }, [client])

  return <WebSocketContext.Provider value={{ client }}>{children}</WebSocketContext.Provider>
}

export function useWebSocketClient(): WebSocketClient {
  const context = useContext(WebSocketContext)
  if (context === undefined) {
    throw new Error("useWebSocketClient must be used within WebSocketProvider")
  }
  return context.client
}

export function useWebSocketState(): WebSocketState {
  const client = useWebSocketClient()
  return useSyncExternalStore(client.subscribe, client.getState, client.getState)
}
