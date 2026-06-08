import { createRouter, createWebHashHistory } from 'vue-router'
import BackupsView from './pages/BackupsView.vue'
import DashboardView from './pages/DashboardView.vue'
import JobsView from './pages/JobsView.vue'
import LogsView from './pages/LogsView.vue'
import SettingsView from './pages/SettingsView.vue'

export const routes = [
  { path: '/', name: 'Dashboard', component: DashboardView },
  { path: '/jobs', name: 'Jobs', component: JobsView },
  { path: '/backups', name: 'Backups', component: BackupsView },
  { path: '/settings', name: 'Settings', component: SettingsView },
  { path: '/logs', name: 'Logs', component: LogsView }
]

export const router = createRouter({
  history: createWebHashHistory(),
  routes
})
