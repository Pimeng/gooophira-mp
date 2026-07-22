import type { ApiError } from "@/lib/api/types"
import { env } from "@/lib/env"

export type AdminTokenProvider = () => string | undefined

export class ApiRequestError extends Error {
  readonly status: number
  readonly code: string
  readonly details: ApiError | undefined

  constructor(status: number, error: string, message?: string, details?: ApiError) {
    super(message ?? error)
    this.name = "ApiRequestError"
    this.status = status
    this.code = error
    this.details = details
  }
}

export interface ApiClientOptions {
  baseUrl?: string
  getAdminToken: AdminTokenProvider
  onUnauthorized?: (error: ApiRequestError) => void
  fetch?: typeof globalThis.fetch
}

function normalizeBaseUrl(baseUrl: string): string {
  const normalized = baseUrl.replace(/\/+$/, "")
  return normalized.endsWith("/api") ? normalized : `${normalized}/api`
}

function joinPath(baseUrl: string, path: string): string {
  return `${baseUrl}/${path.replace(/^\/+/, "")}`
}

async function readError(response: Response): Promise<ApiError> {
  let body: Partial<ApiError> = {}
  try {
    body = (await response.json()) as Partial<ApiError>
  } catch {
    // Non-JSON HTTP errors are represented by the HTTP status below.
  }

  return {
    ok: false,
    error: body.error ?? `http-${response.status}`,
    ...(body.message ? { message: body.message } : {}),
    ...(body.invalidKeys ? { invalidKeys: body.invalidKeys } : {}),
    ...(body.startupOnlyKeys ? { startupOnlyKeys: body.startupOnlyKeys } : {}),
    ...(body.unsupportedKeys ? { unsupportedKeys: body.unsupportedKeys } : {}),
    ...(body.managedKeys ? { managedKeys: body.managedKeys } : {}),
  }
}

function isApiError(value: unknown): value is ApiError {
  return (
    typeof value === "object" &&
    value !== null &&
    "ok" in value &&
    (value as { ok?: unknown }).ok === false &&
    "error" in value &&
    typeof (value as { error?: unknown }).error === "string"
  )
}

function assertApiSuccess<T>(body: T, status: number): T {
  if (isApiError(body)) {
    const errorBody = body
    throw new ApiRequestError(status, errorBody.error, errorBody.message, errorBody)
  }
  return body
}

export function createApiClient(options: ApiClientOptions) {
  const requestFetch = options.fetch ?? globalThis.fetch
  const baseUrl = normalizeBaseUrl(options.baseUrl ?? env.apiBase)

  async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
    const headers = new Headers(init.headers)
    headers.set("Accept", "application/json")

    const token = options.getAdminToken()
    if (token) {
      headers.set("X-Admin-Token", token)
    }

    if (init.body !== undefined && !headers.has("Content-Type")) {
      headers.set("Content-Type", "application/json")
    }

    let response: Response
    try {
      response = await requestFetch(joinPath(baseUrl, path), {
        ...init,
        headers,
      })
    } catch (error) {
      throw new ApiRequestError(0, "network-error", error instanceof Error ? error.message : undefined)
    }

    if (!response.ok) {
      const errorBody = await readError(response)
      const error = new ApiRequestError(
        response.status,
        errorBody.error,
        errorBody.message,
        errorBody,
      )
      if (response.status === 401 || response.status === 403 || response.status === 429) {
        options.onUnauthorized?.(error)
      }
      throw error
    }

    const body = (await response.json()) as T
    return assertApiSuccess(body, response.status)
  }

  return {
    get<T>(path: string, init?: RequestInit) {
      return request<T>(path, { ...init, method: "GET" })
    },
    post<T>(path: string, body?: unknown, init?: RequestInit) {
      return request<T>(path, {
        ...init,
        method: "POST",
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      })
    },
    delete<T>(path: string, init?: RequestInit) {
      return request<T>(path, { ...init, method: "DELETE" })
    },
  }
}

export type ApiClient = ReturnType<typeof createApiClient>
