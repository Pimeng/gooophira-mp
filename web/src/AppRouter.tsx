import { BrowserRouter, Navigate, Route, Routes } from "react-router-dom"

import { AppShell } from "@/components/AppShell"
import { BansPage } from "@/pages/BansPage"
import { DashboardPage } from "@/pages/DashboardPage"
import { PlayerDetailPage } from "@/pages/PlayerDetailPage"
import { PlayersPage } from "@/pages/PlayersPage"
import { RoomDetailPage } from "@/pages/RoomDetailPage"
import { RoomsPage } from "@/pages/RoomsPage"
import { SettingsPage } from "@/pages/SettingsPage"
import { SystemPage } from "@/pages/SystemPage"

export function AppRouter() {
  return (
    <BrowserRouter>
      <AppShell>
        <Routes>
          <Route path="/dashboard" element={<DashboardPage />} />
          <Route path="/rooms" element={<RoomsPage />} />
          <Route path="/rooms/:id" element={<RoomDetailPage />} />
          <Route path="/players" element={<PlayersPage />} />
          <Route path="/players/:id" element={<PlayerDetailPage />} />
          <Route path="/bans" element={<BansPage />} />
          <Route path="/system" element={<SystemPage />} />
          <Route path="/settings" element={<SettingsPage />} />
          <Route path="*" element={<Navigate to="/dashboard" replace />} />
        </Routes>
      </AppShell>
    </BrowserRouter>
  )
}
