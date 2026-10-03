<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import { api, type Status } from '../api'

const { t, locale } = useI18n()

function when(unix: number) {
  return new Date(unix * 1000).toLocaleString(locale.value)
}
const status = ref<Status>()
const error = ref('')

onMounted(async () => {
  try {
    status.value = await api.status()
  } catch (e) {
    error.value = (e as Error).message
  }
})
</script>

<template>
  <section class="stack">
    <h2>{{ t('dashboard.title') }}</h2>
    <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    <template v-if="status">
      <div v-if="status.claimCode" class="card stack">
        <div class="row">
          <i class="pi pi-link" aria-hidden="true" />
          <b>{{ t('dashboard.waiting') }}</b>
        </div>
        <p class="muted">{{ t('dashboard.waitingHint') }}</p>
        <div class="field">
          <label>{{ t('dashboard.claimCode') }}</label>
          <code class="mono claim">{{ status.claimCode }}</code>
        </div>
      </div>
      <div v-else-if="status.panel" class="card stack">
        <div class="row">
          <Tag severity="success" :value="t('dashboard.registered')" />
          <span class="mono url">{{ status.panel.url }}</span>
        </div>
        <Message v-if="status.panelError" severity="warn" :closable="false">
          {{ t('dashboard.panelError') }}: {{ status.panelError }}
        </Message>
        <dl>
          <dt>{{ t('dashboard.panelVersion') }}</dt>
          <dd class="mono">{{ status.panel.version || '—' }}</dd>
          <dt>{{ t('dashboard.since') }}</dt>
          <dd>{{ when(status.panel.registeredAt) }}</dd>
          <dt v-if="status.accounts !== undefined">{{ t('dashboard.accounts') }}</dt>
          <dd v-if="status.accounts !== undefined" class="mono">
            {{ status.accounts.toLocaleString(locale) }}
          </dd>
          <dt>{{ t('dashboard.scopes') }}</dt>
          <dd>
            <Tag
              v-for="s in status.scopes"
              :key="s"
              :value="s"
              severity="secondary"
              class="scope"
            />
          </dd>
        </dl>
      </div>
      <dl class="card">
        <dt>{{ t('dashboard.version') }}</dt>
        <dd class="mono">{{ status.version }}</dd>
        <dt>{{ t('dashboard.database') }}</dt>
        <dd class="mono">{{ status.database }}</dd>
      </dl>
      <p class="muted">{{ t('dashboard.next') }}</p>
    </template>
  </section>
</template>

<style scoped>
h2 {
  margin: 0;
}
.row {
  display: flex;
  gap: 0.6rem;
  align-items: center;
  flex-wrap: wrap;
}
.url {
  overflow-wrap: anywhere;
}
.claim {
  font-size: 1.4rem;
  letter-spacing: 0.08em;
  user-select: all;
}
dl {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.5rem 1.5rem;
  margin: 0;
}
dt {
  color: var(--notif-muted);
}
dd {
  margin: 0;
  min-width: 0;
}
.scope {
  margin-inline-end: 0.35rem;
}
</style>
