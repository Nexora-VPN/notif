<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Select from 'primevue/select'
import InputText from 'primevue/inputtext'
import InputNumber from 'primevue/inputnumber'
import ToggleSwitch from 'primevue/toggleswitch'
import Button from 'primevue/button'
import { api, type DeliverySettings } from '../api'
import { locales } from '../i18n'

const { t } = useI18n()
const toast = useToast()
const form = ref<DeliverySettings>()
const serverZone = ref('')
const busy = ref(false)

const zones: string[] = (() => {
  try {
    return (Intl as unknown as { supportedValuesOf(k: string): string[] }).supportedValuesOf(
      'timeZone',
    )
  } catch {
    return ['UTC', 'Asia/Tehran', 'Europe/Moscow', 'Asia/Shanghai']
  }
})()

onMounted(async () => {
  try {
    const r = await api.deliverySettings()
    form.value = r.settings
    serverZone.value = r.serverZone
  } catch (e) {
    toast.add({
      severity: 'error',
      summary: t('common.error'),
      detail: (e as Error).message,
      life: 5000,
    })
  }
})

async function save() {
  if (!form.value) return
  busy.value = true
  try {
    await api.saveDeliverySettings(form.value)
    toast.add({ severity: 'success', summary: t('common.saved'), life: 2500 })
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
</script>

<template>
  <section class="stack">
    <h2>{{ t('settings.title') }}</h2>
    <form v-if="form" class="card stack" @submit.prevent="save">
      <div class="field">
        <label>{{ t('settings.timeZone') }}</label>
        <Select v-model="form.timeZone" :options="['', ...zones]" filter show-clear>
          <template #value="{ value }">{{ value || serverZone }}</template>
          <template #option="{ option }">{{ option || serverZone }}</template>
        </Select>
        <small class="muted">{{ t('settings.timeZoneHelp', { zone: serverZone }) }}</small>
      </div>
      <div class="field">
        <label class="inline">
          <ToggleSwitch v-model="form.quietEnabled" />
          {{ t('settings.quiet') }}
        </label>
        <div class="row">
          <label class="inline">
            {{ t('settings.from') }}
            <InputText
              v-model="form.quietFrom"
              type="time"
              :disabled="!form.quietEnabled"
              dir="ltr"
            />
          </label>
          <label class="inline">
            {{ t('settings.to') }}
            <InputText
              v-model="form.quietTo"
              type="time"
              :disabled="!form.quietEnabled"
              dir="ltr"
            />
          </label>
        </div>
        <small class="muted">{{ t('settings.quietHelp') }}</small>
      </div>
      <div class="field">
        <label>{{ t('settings.language') }}</label>
        <Select
          v-model="form.language"
          :options="[...locales]"
          option-label="name"
          option-value="code"
        />
        <small class="muted">{{ t('settings.languageHelp') }}</small>
      </div>
      <div class="field">
        <label for="ret">{{ t('settings.retention') }}</label>
        <InputNumber v-model="form.retentionDays" input-id="ret" :min="1" :max="3650" />
      </div>
      <div>
        <Button type="submit" :label="t('common.save')" :loading="busy" />
      </div>
    </form>
  </section>
</template>

<style scoped>
h2 {
  margin: 0;
}
form {
  max-width: 34rem;
}
.inline {
  display: inline-flex;
  align-items: center;
  gap: 0.6rem;
  color: inherit;
  font-size: 1rem;
}
.row {
  display: flex;
  gap: 1.25rem;
  flex-wrap: wrap;
}
</style>
