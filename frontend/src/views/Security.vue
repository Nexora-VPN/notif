<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import SecretInput from '../components/SecretInput.vue'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import QrcodeVue from 'qrcode.vue'
import { api, type Me } from '../api'

const { t } = useI18n()
const toast = useToast()
const me = ref<Me>()
const current = ref('')
const next = ref('')
const enrol = ref<{ secret: string; uri: string }>()
const code = ref('')
const offPassword = ref('')
const busy = ref(false)

async function load() {
  me.value = await api.me()
}
onMounted(load)

async function run(f: () => Promise<void>, done: string) {
  busy.value = true
  try {
    await f()
    toast.add({ severity: 'success', summary: done, life: 3000 })
  } catch (e) {
    toast.add({
      severity: 'error',
      summary: t('common.error'),
      detail: (e as Error).message,
      life: 5000,
    })
  } finally {
    busy.value = false
  }
}

function changePassword() {
  return run(async () => {
    await api.password(current.value, next.value)
    current.value = ''
    next.value = ''
  }, t('security.changed'))
}

async function startEnrol() {
  try {
    enrol.value = await api.totpEnrol()
  } catch (e) {
    toast.add({
      severity: 'error',
      summary: t('common.error'),
      detail: (e as Error).message,
      life: 5000,
    })
  }
}

function confirmEnrol() {
  return run(async () => {
    await api.totpConfirm(code.value)
    enrol.value = undefined
    code.value = ''
    await load()
  }, t('security.enabled'))
}

function disable() {
  return run(async () => {
    await api.totpDisable(offPassword.value)
    offPassword.value = ''
    await load()
  }, t('security.disabled'))
}
</script>

<template>
  <section class="stack">
    <h2>{{ t('security.title') }}</h2>
    <form class="card stack" @submit.prevent="changePassword">
      <h3>{{ t('security.password') }}</h3>
      <div class="field">
        <label for="cur">{{ t('security.current') }}</label>
        <SecretInput v-model="current" input-id="cur" autocomplete="current-password" own />
      </div>
      <div class="field">
        <label for="new">{{ t('security.new') }}</label>
        <SecretInput v-model="next" input-id="new" autocomplete="new-password" own />
        <small class="muted">{{ t('security.newHint') }}</small>
      </div>
      <div>
        <Button
          type="submit"
          :label="t('security.change')"
          :loading="busy"
          :disabled="!current || next.length < 10"
        />
      </div>
    </form>

    <div v-if="me" class="card stack">
      <div class="row">
        <h3>{{ t('security.totp') }}</h3>
      </div>
      <p class="muted">{{ me.totpEnabled ? t('security.totpOn') : t('security.totpOff') }}</p>
      <template v-if="!me.totpEnabled">
        <div v-if="!enrol">
          <Button :label="t('security.enable')" icon="pi pi-shield" @click="startEnrol" />
        </div>
        <form v-else class="stack" @submit.prevent="confirmEnrol">
          <p>{{ t('security.scan') }}</p>
          <div class="qr"><QrcodeVue :value="enrol.uri" :size="180" level="M" /></div>
          <div class="field">
            <label>{{ t('security.key') }}</label>
            <code class="mono key">{{ enrol.secret }}</code>
          </div>
          <div class="row">
            <InputText
              v-model="code"
              inputmode="numeric"
              autocomplete="one-time-code"
              maxlength="6"
              class="mono"
              :aria-label="t('login.code')"
            />
            <Button
              type="submit"
              :label="t('security.confirm')"
              :loading="busy"
              :disabled="code.length !== 6"
            />
          </div>
        </form>
      </template>
      <form v-else class="row" @submit.prevent="disable">
        <SecretInput
          v-model="offPassword"
          :placeholder="t('security.disablePassword')"
          autocomplete="current-password"
          own
        />
        <Button
          type="submit"
          :label="t('security.disable')"
          severity="danger"
          outlined
          :loading="busy"
          :disabled="!offPassword"
        />
      </form>
    </div>
  </section>
</template>

<style scoped>
h2,
h3 {
  margin: 0;
}
.row {
  display: flex;
  gap: 0.6rem;
  align-items: center;
  flex-wrap: wrap;
}
.qr {
  background: #fff;
  padding: 0.75rem;
  border-radius: 8px;
  width: fit-content;
}
.key {
  overflow-wrap: anywhere;
  user-select: all;
}
</style>
