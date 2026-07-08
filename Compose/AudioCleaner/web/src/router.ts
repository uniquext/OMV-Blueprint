import { createRouter, createWebHashHistory } from 'vue-router'
import BackupsView from './pages/BackupsView.vue'
import DashboardView from './pages/DashboardView.vue'
import HistoryView from './pages/HistoryView.vue'
import LogsView from './pages/LogsView.vue'
import SettingsView from './pages/SettingsView.vue'

export const routes = [
  { path: '/', name: 'Dashboard', component: DashboardView, meta: { titleKey: 'navDashboard' } },
  { path: '/history', name: 'History', component: HistoryView, meta: { titleKey: 'navHistory' } },
  { path: '/backups', name: 'Backups', component: BackupsView, meta: { titleKey: 'navBackups' } },
  { path: '/settings', name: 'Settings', component: SettingsView, meta: { titleKey: 'navSettings' } },
  { path: '/logs', name: 'Logs', component: LogsView, meta: { titleKey: 'navLogs' } }
]

export const router = createRouter({
  history: createWebHashHistory(),
  routes
})
