import { createRouter, createWebHistory } from 'vue-router'
import { api, whenSignedOut } from './api'
import Login from './views/Login.vue'
import Layout from './views/Layout.vue'
import Dashboard from './views/Dashboard.vue'
import Security from './views/Security.vue'

export const router = createRouter({
  // Relative to wherever Notif is served, the way its assets are.
  history: createWebHistory(new URL('.', document.baseURI).pathname),
  routes: [
    { path: '/login', name: 'login', component: Login, meta: { public: true } },
    {
      path: '/',
      component: Layout,
      children: [
        { path: '', name: 'dashboard', component: Dashboard },
        { path: 'security', name: 'security', component: Security },
      ],
    },
  ],
})

let checked = false

router.beforeEach(async (to) => {
  if (to.meta.public) return true
  if (checked) return true
  try {
    await api.me()
    checked = true
    return true
  } catch {
    return { name: 'login', query: to.fullPath === '/' ? {} : { next: to.fullPath } }
  }
})

whenSignedOut(() => {
  checked = false
  if (router.currentRoute.value.name !== 'login') void router.push({ name: 'login' })
})

export function signedIn() {
  checked = true
}
