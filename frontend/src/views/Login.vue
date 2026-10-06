<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import InputText from 'primevue/inputtext'
import SecretInput from '../components/SecretInput.vue'
import Button from 'primevue/button'
import Message from 'primevue/message'
import Select from 'primevue/select'
import { api } from '../api'
import { signedIn } from '../router'
import { locales, setLocale, type Locale } from '../i18n'

const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()

const username = ref('')
const password = ref('')
const code = ref('')
const mfaToken = ref('')
const error = ref('')
const busy = ref(false)

async function done() {
  signedIn()
  const next = typeof route.query.next === 'string' ? route.query.next : '/'
  await router.replace(next.startsWith('/') ? next : '/')
}

async function submit() {
  busy.value = true
  error.value = ''
  try {
    if (mfaToken.value) {
      await api.loginMFA(mfaToken.value, code.value)
      await done()
      return
    }
    const res = await api.login(username.value, password.value)
    if ('mfa' in res) {
      mfaToken.value = res.token
      return
    }
    await done()
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}

function back() {
  mfaToken.value = ''
  code.value = ''
  error.value = ''
}

function pick(l: Locale) {
  setLocale(l)
}
</script>

<template>
  <main class="login">
    <form class="card stack" @submit.prevent="submit">
      <header class="brand">
        <i class="pi pi-bell" aria-hidden="true" />
        <div>
          <h1>{{ t('app.name') }}</h1>
          <p class="muted">{{ t('app.tagline') }}</p>
        </div>
      </header>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
      <template v-if="!mfaToken">
        <div class="field">
          <label for="u">{{ t('login.username') }}</label>
          <InputText id="u" v-model="username" autocomplete="username" autofocus />
        </div>
        <div class="field">
          <label for="p">{{ t('login.password') }}</label>
          <SecretInput v-model="password" input-id="p" autocomplete="current-password" own />
        </div>
        <Button type="submit" :label="t('login.submit')" :loading="busy" />
      </template>
      <template v-else>
        <div class="field">
          <label for="c">{{ t('login.code') }}</label>
          <InputText
            id="c"
            v-model="code"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="6"
            class="mono"
            autofocus
          />
        </div>
        <Button type="submit" :label="t('login.verify')" :loading="busy" />
        <Button type="button" :label="t('login.back')" text @click="back" />
      </template>
      <p class="muted hint">{{ t('login.forgot') }}</p>
      <Select
        :model-value="locale"
        :options="[...locales]"
        option-label="name"
        option-value="code"
        :aria-label="t('nav.language')"
        @update:model-value="pick"
      />
    </form>
  </main>
</template>

<style scoped>
.login {
  min-height: 100vh;
  display: grid;
  place-items: center;
  padding: 1rem;
  box-sizing: border-box;
}
form {
  width: min(24rem, 100%);
  box-sizing: border-box;
}
.brand {
  display: flex;
  gap: 0.75rem;
  align-items: center;
}
.brand .pi {
  font-size: 1.75rem;
  color: var(--p-primary-500);
}
h1 {
  margin: 0;
  font-size: 1.25rem;
}
.brand p {
  margin: 0;
}
.hint {
  font-size: 0.8rem;
  margin: 0;
  overflow-wrap: anywhere;
}
</style>
