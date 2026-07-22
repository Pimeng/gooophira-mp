import { ApiRequestError } from "@/lib/api/client"
import type {
  ApiError,
  OtpMode,
  OtpRequestResponse,
  OtpVerifyRequest,
  OtpVerifyResponse,
} from "@/lib/api/types"
import { env } from "@/lib/env"

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
    // The server can return a non-JSON error response.
  }
  return {
    ok: false,
    error: body.error ?? `http-${response.status}`,
    ...(body.message ? { message: body.message } : {}),
  }
}

export interface OtpClientOptions {
  baseUrl?: string
  fetch?: typeof globalThis.fetch
}

export function createOtpApi(options: OtpClientOptions = {}) {
  const requestFetch = options.fetch ?? globalThis.fetch
  const baseUrl = normalizeBaseUrl(options.baseUrl ?? env.apiBase)

  async function post<T>(path: string, body: unknown): Promise<T> {
    let response: Response
    try {
      response = await requestFetch(joinPath(baseUrl, path), {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/json",
        },
        body: JSON.stringify(body),
      })
    } catch (error) {
      throw new ApiRequestError(0, "network-error", error instanceof Error ? error.message : undefined)
    }

    if (!response.ok) {
      const errorBody = await readError(response)
      throw new ApiRequestError(response.status, errorBody.error, errorBody.message, errorBody)
    }

    const result: unknown = await response.json()
    if (
      typeof result === "object" &&
      result !== null &&
      "ok" in result &&
      (result as { ok?: unknown }).ok === false &&
      "error" in result &&
      typeof (result as { error?: unknown }).error === "string"
    ) {
      const errorBody = result as ApiError
      throw new ApiRequestError(response.status, errorBody.error, errorBody.message, errorBody)
    }
    return result as T
  }

  return {
    request(mode: OtpMode = "otp") {
      return post<OtpRequestResponse>("/admin/otp/request", { mode })
    },
    verify(request: OtpVerifyRequest) {
      return post<OtpVerifyResponse>("/admin/otp/verify", request)
    },
  }
}
