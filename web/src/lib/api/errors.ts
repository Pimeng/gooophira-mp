import type { ApiError } from "@/lib/api/types"

export class ApiRequestError extends Error {
  readonly status: number
  readonly code: string
  readonly details: ApiError | undefined

  public constructor(status: number, code: string, message?: string, details?: ApiError) {
    super(message ?? code)
    this.name = "ApiRequestError"
    this.status = status
    this.code = code
    this.details = details
  }
}

export function isApiError(value: unknown): value is ApiError {
  return (
    typeof value === "object" &&
    value !== null &&
    "ok" in value &&
    (value as { ok?: unknown }).ok === false &&
    "error" in value &&
    typeof (value as { error?: unknown }).error === "string"
  )
}
