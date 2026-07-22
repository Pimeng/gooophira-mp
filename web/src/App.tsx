import { AppRouter } from "@/AppRouter"
import { AppProviders } from "@/providers/AppProviders"

export function App() {
  return (
    <AppProviders>
      <AppRouter />
    </AppProviders>
  )
}
