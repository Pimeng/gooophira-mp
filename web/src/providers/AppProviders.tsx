import type { ReactNode } from "react"
import { createContext, useContext, useState } from "react"

import { createApiClient, type ApiClient } from "@/lib/api/client"
import type { AdminTokenProvider } from "@/lib/api/token-provider"
import { createMemoryTokenProvider } from "@/lib/api/token-provider"
import { QueryProvider } from "@/providers/QueryProvider"
import { WebSocketProvider } from "@/providers/WebSocketProvider"

const ApiClientContext = createContext<ApiClient | undefined>(undefined)

export interface AppProvidersProps {
  children: ReactNode
  tokenProvider?: AdminTokenProvider
  wsBase?: string
}

export function AppProviders({ children, tokenProvider, wsBase }: AppProvidersProps) {
  const [provider] = useState(() => tokenProvider ?? createMemoryTokenProvider())
  const [client] = useState(() => createApiClient({ getAdminToken: () => provider.getToken() }))

  return (
    <QueryProvider>
      <ApiClientContext.Provider value={client}>
        <WebSocketProvider tokenProvider={provider} wsBase={wsBase}>
          {children}
        </WebSocketProvider>
      </ApiClientContext.Provider>
    </QueryProvider>
  )
}

export function useApiClient(): ApiClient {
  const client = useContext(ApiClientContext)
  if (client === undefined) {
    throw new Error("useApiClient must be used within AppProviders")
  }
  return client
}
