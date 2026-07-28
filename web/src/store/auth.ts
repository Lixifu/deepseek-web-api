import { defineStore } from 'pinia'
import http from '../api/http'

export const useAuth = defineStore('auth', {
  state: () => ({
    token: localStorage.getItem('admin_token') || '',
    username: localStorage.getItem('admin_username') || '',
    role: localStorage.getItem('admin_role') || 'viewer'
  }),
  getters: {
    canManage: (state) => state.role === 'admin' || state.role === 'superadmin',
    isSuperAdmin: (state) => state.role === 'superadmin'
  },
  actions: {
    async login(username: string, password: string) {
      const { data } = await http.post('/admin/login', { username, password })
      this.token = data.token
      this.username = data.username
      this.role = data.role
      localStorage.setItem('admin_token', data.token)
      localStorage.setItem('admin_username', data.username)
      localStorage.setItem('admin_role', data.role)
    },
    logout() {
      this.token = ''
      this.username = ''
      this.role = 'viewer'
      localStorage.removeItem('admin_token')
      localStorage.removeItem('admin_username')
      localStorage.removeItem('admin_role')
    }
  }
})
