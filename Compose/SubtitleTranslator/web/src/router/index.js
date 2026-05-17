import { createRouter, createWebHistory } from 'vue-router'

const Dashboard = () => import('../views/Dashboard.vue')
const History = () => import('../views/History.vue')
const Config = () => import('../views/Config.vue')

export const routes = [
  {
    path: '/',
    redirect: '/dashboard'
  },
  {
    path: '/dashboard',
    name: 'Dashboard',
    component: Dashboard
  },
  {
    path: '/history',
    name: 'History',
    component: History
  },
  {
    path: '/config',
    name: 'Config',
    component: Config
  },
  {
    path: '/:pathMatch(.*)*',
    redirect: '/dashboard'
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

export default router
