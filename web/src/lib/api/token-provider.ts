export interface AdminTokenProvider {
  getToken(): string | undefined
}

export interface MemoryTokenProvider extends AdminTokenProvider {
  setToken(token: string): void
  clearToken(): void
}

export function createMemoryTokenProvider(initialToken?: string): MemoryTokenProvider {
  let token = initialToken

  return {
    getToken(): string | undefined {
      return token
    },
    setToken(nextToken: string): void {
      token = nextToken
    },
    clearToken(): void {
      token = undefined
    },
  }
}
