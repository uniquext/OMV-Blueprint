import { createRouter, createWebHashHistory } from 'vue-router'
import DashboardView from './pages/DashboardView.vue'
import MediaFilesView from './pages/MediaFilesView.vue'
import HistoryView from './pages/HistoryView.vue'
import LogsView from './pages/LogsView.vue'
import SettingsView from './pages/SettingsView.vue'
import RecoveryView from './pages/RecoveryView.vue'

export const routes = [
  { path: '/', name: 'Dashboard', component: DashboardView, meta: { titleKey: 'navDashboard' } },
  { path: '/files', name: 'Files', component: MediaFilesView, meta: { titleKey: 'navFiles' } },
  { path: '/history', name: 'History', component: HistoryView, meta: { titleKey: 'navHistory' } },
  { path: '/recovery', name: 'Recovery', component: RecoveryView, meta: { titleKey: 'navRecovery' } },
  { path: '/logs', name: 'Logs', component: LogsView, meta: { titleKey: 'navLogs' } },
  { path: '/settings', name: 'Settings', component: SettingsView, meta: { titleKey: 'navSettings' } }
]

export const router = createRouter({
  history: createWebHashHistory(),
  routes
})
