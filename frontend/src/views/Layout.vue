<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import Button from 'primevue/button'
import Select from 'primevue/select'
import { api } from '../api'
import { locales, setLocale, type Locale } from '../i18n'

const { t, locale } = useI18n()
const router = useRouter()
const open = ref(false)

const nav = computed(() => [
  { to: { name: 'dashboard' }, icon: 'pi pi-home', label: t('nav.dashboard') },
  { to: { name: 'channels' }, icon: 'pi pi-send', label: t('nav.channels') },
  { to: { name: 'log' }, icon: 'pi pi-list', label: t('nav.log') },
  { to: { name: 'settings' }, icon: 'pi pi-cog', label: t('nav.settings') },
  { to: { name: 'security' }, icon: 'pi pi-shield', label: t('nav.security') },
])

async function signOut() {
  try {
    await api.logout()
  } finally {
    await router.push({ name: 'login' })
  }
}

function pick(l: Locale) {
  setLocale(l)
}
</script>

<template>
  <div class="shell">
    <aside :class="{ open }">
      <div class="brand">
        <i class="pi pi-bell" aria-hidden="true" />
        <b>{{ t('app.name') }}</b>
      </div>
      <nav>
        <RouterLink v-for="n in nav" :key="n.label" :to="n.to" @click="open = false">
          <i :class="n.icon" aria-hidden="true" />
          <span>{{ n.label }}</span>
        </RouterLink>
      </nav>
      <div class="foot">
        <Select
          :model-value="locale"
          :options="[...locales]"
          option-label="name"
          option-value="code"
          :aria-label="t('nav.language')"
          fluid
          @update:model-value="pick"
        />
        <Button
          :label="t('nav.signOut')"
          icon="pi pi-sign-out"
          severity="secondary"
          text
          @click="signOut"
        />
      </div>
    </aside>
    <div class="main">
      <header class="bar">
        <Button
          icon="pi pi-bars"
          text
          severity="secondary"
          :aria-label="t('nav.dashboard')"
          @click="open = !open"
        />
        <b>{{ t('app.name') }}</b>
      </header>
      <main class="content">
        <RouterView />
      </main>
    </div>
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  min-height: 100vh;
}
aside {
  width: 15rem;
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 1rem;
  padding: 1rem;
  background: var(--notif-surface);
  border-inline-end: 1px solid var(--notif-border);
  box-sizing: border-box;
}
.brand {
  display: flex;
  gap: 0.6rem;
  align-items: center;
  padding: 0.25rem 0.5rem 0.75rem;
}
.brand .pi {
  color: var(--p-primary-500);
  font-size: 1.25rem;
}
nav {
  display: flex;
  flex-direction: column;
  gap: 0.25rem;
  flex: 1;
}
nav a {
  display: flex;
  gap: 0.6rem;
  align-items: center;
  padding: 0.55rem 0.75rem;
  border-radius: 8px;
  color: inherit;
  text-decoration: none;
}
nav a:hover {
  background: var(--notif-bg);
}
nav a.router-link-exact-active {
  background: var(--p-primary-100);
  color: var(--p-primary-900);
}
.foot {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.main {
  flex: 1;
  min-width: 0;
}
.bar {
  display: none;
}
.content {
  padding: 1.5rem;
  max-width: 64rem;
}
@media (max-width: 800px) {
  aside {
    display: none;
  }
  aside.open {
    display: flex;
    position: fixed;
    inset-block: 0;
    inset-inline-start: 0;
    z-index: 10;
    box-shadow: 0 0 24px rgb(0 0 0 / 0.18);
  }
  .bar {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 1rem;
    border-bottom: 1px solid var(--notif-border);
    background: var(--notif-surface);
  }
  .content {
    padding: 1rem;
  }
}
</style>
