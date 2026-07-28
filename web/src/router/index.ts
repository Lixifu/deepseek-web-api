import { createRouter, createWebHistory } from 'vue-router'
import { useAuth } from '../store/auth'

const routes = [
  { path: '/login', component: () => import('../views/Login.vue'), meta: { public: true } },
  {
    path: '/',
    component: () => import('../layouts/Main.vue'),
    children: [
      { path: '', redirect: '/dashboard' },
      { path: 'dashboard', component: () => import('../views/Dashboard.vue') },
      { path: 'conversations', component: () => import('../views/Conversations.vue') },
      { path: 'accounts', component: () => import('../views/Accounts.vue') },
      { path: 'api-keys', component: () => import('../views/ApiKeys.vue') },
      { path: 'admins', component: () => import('../views/Admins.vue'), meta: { roles: ['superadmin'] } },
      { path: 'audit-logs', component: () => import('../views/AuditLogs.vue'), meta: { roles: ['superadmin'] } }
    ]
  }
]

const router = createRouter({ history: createWebHistory(), routes })

router.beforeEach((to) => {
  const auth = useAuth()
  if (!to.meta.public && !auth.token) {
    return '/login'
  }
  const roles = to.meta.roles as string[] | undefined
  if (roles && !roles.includes(auth.role)) {
    return '/dashboard'
  }
})

export default router
