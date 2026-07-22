import type { AdminChart, AdminRoom, AdminRoomHost, RoomLogLine } from "@/lib/api/types"

export type WsClientMessage =
  | { type: "ping" }
  | { type: "subscribe"; roomId: string }
  | { type: "unsubscribe" }
  | { type: "admin_subscribe"; token: string }
  | { type: "admin_unsubscribe" }
  | { type: "console_subscribe"; token: string }
  | { type: "console_unsubscribe" }

export interface RoomUpdateUser {
  id: number
  name: string
  is_ready: boolean
  connected: boolean
}

export interface RoomUpdateMonitor {
  id: number
  name: string
  connected: boolean
}

export interface RoomUpdateData {
  roomid: string
  state: string
  locked: boolean
  cycle: boolean
  live: boolean
  chart: AdminChart | null
  host: AdminRoomHost
  users: RoomUpdateUser[]
  monitors: RoomUpdateMonitor[]
  recent_logs: RoomLogLine[]
}

export interface WsPongEvent {
  type: "pong"
}

export interface WsSubscribedEvent {
  type: "subscribed"
  roomId: string
}

export interface WsUnsubscribedEvent {
  type: "unsubscribed"
}

export interface WsAdminSubscribedEvent {
  type: "admin_subscribed"
}

export interface WsAdminUnsubscribedEvent {
  type: "admin_unsubscribed"
}

export interface WsConsoleSubscribedEvent {
  type: "console_subscribed"
  data: { lines: ConsoleLogLine[] }
}

export interface WsConsoleUnsubscribedEvent {
  type: "console_unsubscribed"
}

export interface WsErrorEvent {
  type: "error"
  message: string
}

export interface RoomUpdateEvent {
  type: "room_update"
  data: RoomUpdateData
}

export interface AdminUpdateEvent {
  type: "admin_update"
  data: {
    timestamp: number
    changes: {
      rooms: AdminRoom[]
      total_rooms: number
    }
  }
}

export interface ConsoleLogLine {
  level: string
  message: string
  timestamp: number
}

export interface ConsoleLogEvent {
  type: "console_log"
  data: ConsoleLogLine
}

export type WsServerEvent =
  | WsPongEvent
  | WsSubscribedEvent
  | WsUnsubscribedEvent
  | WsAdminSubscribedEvent
  | WsAdminUnsubscribedEvent
  | WsConsoleSubscribedEvent
  | WsConsoleUnsubscribedEvent
  | WsErrorEvent
  | RoomUpdateEvent
  | AdminUpdateEvent
  | ConsoleLogEvent

export function encodeClientMessage(message: WsClientMessage): string {
  return JSON.stringify(message)
}

export function decodeServerEvent(data: string): WsServerEvent | undefined {
  let value: unknown

  try {
    value = JSON.parse(data)
  } catch {
    return undefined
  }

  if (typeof value !== "object" || value === null || !("type" in value)) {
    return undefined
  }

  const type = (value as { type?: unknown }).type
  if (
    type === "pong" ||
    type === "subscribed" ||
    type === "unsubscribed" ||
    type === "admin_subscribed" ||
    type === "admin_unsubscribed" ||
    type === "console_subscribed" ||
    type === "console_unsubscribed" ||
    type === "error" ||
    type === "room_update" ||
    type === "admin_update" ||
    type === "console_log"
  ) {
    return value as WsServerEvent
  }

  return undefined
}
