import type { AdminTokenProvider } from "@/lib/api/token-provider"
import { env } from "@/lib/env"
import {
  decodeServerEvent,
  encodeClientMessage,
  type WsClientMessage,
  type WsServerEvent,
} from "@/lib/ws/protocol"
import { ReconnectController } from "@/lib/ws/reconnect"
import {
  WebSocketStateMachine,
  type WebSocketState,
} from "@/lib/ws/state-machine"

export interface WebSocketClientOptions {
  wsBase?: string
  tokenProvider: AdminTokenProvider
  onEvent?: (event: WsServerEvent) => void
  onStateChange?: (state: WebSocketState) => void
  WebSocket?: typeof globalThis.WebSocket
  pingIntervalMs?: number
  pongTimeoutMs?: number
}

export interface WebSocketClient {
  connect(): void
  acquireRoom(roomId: string): () => void
  acquireAdmin(): () => void
  acquireConsole(): () => void
  subscribe(listener: (state: WebSocketState) => void): () => void
  getState(): WebSocketState
  destroy(): void
}

export function createWebSocketClient(
  options: WebSocketClientOptions,
): WebSocketClient {
  const WebSocketConstructor = options.WebSocket ?? globalThis.WebSocket
  const stateMachine = new WebSocketStateMachine()
  const reconnect = new ReconnectController()
  const tokenProvider = options.tokenProvider
  const roomRefs = new Map<string, number>()
  const stateUnsubscribe = options.onStateChange
    ? stateMachine.subscribe(options.onStateChange)
    : undefined

  let socket: WebSocket | undefined
  let destroyed = false
  let adminRefs = 0
  let consoleRefs = 0
  let subscribedRoom: string | undefined
  let sentRoom: string | undefined
  let sentAdmin = false
  let sentConsole = false
  let pingTimer: ReturnType<typeof setInterval> | undefined
  let pongTimer: ReturnType<typeof setTimeout> | undefined

  function emit(event: WsServerEvent): void {
    options.onEvent?.(event)
  }

  function clearHeartbeat(): void {
    if (pingTimer !== undefined) {
      clearInterval(pingTimer)
      pingTimer = undefined
    }
    if (pongTimer !== undefined) {
      clearTimeout(pongTimer)
      pongTimer = undefined
    }
  }

  function send(message: WsClientMessage): void {
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(encodeClientMessage(message))
    }
  }

  function stopSocket(): void {
    clearHeartbeat()
    sentRoom = undefined
    sentAdmin = false
    sentConsole = false
    if (socket !== undefined) {
      socket.onclose = null
      socket.onerror = null
      socket.onmessage = null
      socket.onopen = null
      socket.close()
      socket = undefined
    }
  }

  function startHeartbeat(): void {
    clearHeartbeat()
    const interval = options.pingIntervalMs ?? 25_000
    const timeout = options.pongTimeoutMs ?? 10_000

    const ping = (): void => {
      send({ type: "ping" })
      if (pongTimer !== undefined) {
        clearTimeout(pongTimer)
      }
      pongTimer = setTimeout(() => {
        if (socket?.readyState === WebSocket.OPEN) {
          socket.close()
        }
      }, timeout)
    }

    pingTimer = setInterval(ping, interval)
  }

  function activeRoom(): string | undefined {
    return roomRefs.keys().next().value
  }

  function restoreSubscriptions(): void {
    const roomId = activeRoom()
    if (roomId !== undefined && sentRoom !== roomId) {
      if (sentRoom !== undefined) {
        send({ type: "unsubscribe" })
      }
      send({ type: "subscribe", roomId })
      sentRoom = roomId
      subscribedRoom = roomId
    } else if (roomId === undefined) {
      subscribedRoom = undefined
    }

    const token = tokenProvider.getToken()
    if (adminRefs > 0 && !sentAdmin && token !== undefined) {
      send({ type: "admin_subscribe", token })
      sentAdmin = true
    }

    if (consoleRefs > 0 && !sentConsole && token !== undefined) {
      send({ type: "console_subscribe", token })
      sentConsole = true
    }
  }

  function handleEvent(event: WsServerEvent): void {
    if (event.type === "pong") {
      if (pongTimer !== undefined) {
        clearTimeout(pongTimer)
        pongTimer = undefined
      }
    }

    if (event.type === "room_update") {
      const currentRoom = activeRoom()
      if (currentRoom === undefined || event.data.roomid !== currentRoom) {
        return
      }
    }

    emit(event)
  }

  function scheduleReconnect(): void {
    if (destroyed) {
      return
    }
    stateMachine.transition("reconnecting")
    reconnect.schedule(() => {
      if (!destroyed) {
        connect()
      }
    })
  }

  function connect(): void {
    if (destroyed || socket !== undefined) {
      return
    }

    stateMachine.transition("connecting")
    socket = new WebSocketConstructor(options.wsBase ?? env.wsBase)

    socket.onopen = (): void => {
      reconnect.reset()
      stateMachine.transition("open")
      startHeartbeat()
      restoreSubscriptions()
    }

    socket.onmessage = (message): void => {
      if (typeof message.data !== "string") {
        return
      }
      const event = decodeServerEvent(message.data)
      if (event !== undefined) {
        handleEvent(event)
      }
    }

    socket.onerror = (): void => {
      stateMachine.transition("error")
    }

    socket.onclose = (): void => {
      clearHeartbeat()
      socket = undefined
      sentRoom = undefined
      sentAdmin = false
      sentConsole = false
      if (!destroyed) {
        scheduleReconnect()
      }
    }
  }

  function setRoomReference(roomId: string, delta: number): void {
    const current = roomRefs.get(roomId) ?? 0
    const next = current + delta

    if (next <= 0) {
      roomRefs.delete(roomId)
    } else {
      roomRefs.set(roomId, next)
    }

    if (stateMachine.getState() === "open") {
      const nextRoom = activeRoom()
      if (nextRoom !== subscribedRoom) {
        if (subscribedRoom !== undefined) {
          send({ type: "unsubscribe" })
        }
        subscribedRoom = nextRoom
        sentRoom = undefined
        if (nextRoom !== undefined) {
          send({ type: "subscribe", roomId: nextRoom })
          sentRoom = nextRoom
        }
      }
    }
  }

  function acquireRoom(roomId: string): () => void {
    setRoomReference(roomId, 1)
    let released = false
    return (): void => {
      if (!released) {
        released = true
        setRoomReference(roomId, -1)
      }
    }
  }

  function acquireAdmin(): () => void {
    adminRefs += 1
    if (adminRefs === 1 && stateMachine.getState() === "open") {
      restoreSubscriptions()
    }
    let released = false
    return (): void => {
      if (!released) {
        released = true
        adminRefs = Math.max(0, adminRefs - 1)
        if (adminRefs === 0 && sentAdmin) {
          send({ type: "admin_unsubscribe" })
          sentAdmin = false
        }
      }
    }
  }

  function acquireConsole(): () => void {
    consoleRefs += 1
    if (consoleRefs === 1 && stateMachine.getState() === "open") {
      restoreSubscriptions()
    }
    let released = false
    return (): void => {
      if (!released) {
        released = true
        consoleRefs = Math.max(0, consoleRefs - 1)
        if (consoleRefs === 0 && sentConsole) {
          send({ type: "console_unsubscribe" })
          sentConsole = false
        }
      }
    }
  }

  function destroy(): void {
    if (destroyed) {
      return
    }
    destroyed = true
    reconnect.cancel()
    stateUnsubscribe?.()
    stopSocket()
    roomRefs.clear()
    adminRefs = 0
    consoleRefs = 0
    stateMachine.close()
  }

  return {
    connect,
    acquireRoom,
    acquireAdmin,
    acquireConsole,
    subscribe: (listener: (state: WebSocketState) => void): (() => void) =>
      stateMachine.subscribe(listener),
    getState: (): WebSocketState => stateMachine.getState(),
    destroy,
  }
}
