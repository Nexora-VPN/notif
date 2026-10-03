<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import { api, type Status, type Summary } from '../api'

const { t, locale } = useI18n()

function when(unix: number) {
  return new Date(unix * 1000).toLocaleString(locale.value)
}
const status = ref<Status>()
const sum = ref<Summary>()
const error = ref('')

onMounted(async () => {
  try {
    ;[status.value, sum.value] = await Promise.all([api.status(), api.summary()])
  } catch (e) {
    error.value = (e as Error).message
  }
})
</script>

<template>
  <section class="stack">
    <h2>{{ t('dashboard.title') }}</h2>
    <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    <template v-if="sum">
      <Message v-if="!sum.channels" severity="warn" :closable="false">
        {{ t('dashboard.noChannels') }}
        <RouterLink :to="{ name: 'channels' }">{{ t('nav.channels') }}</RouterLink>
      </Message>
      <Message v-if="sum.quiet" severity="info" :closable="false">{{
        t('dashboard.quietNow')
      }}</Message>
      <div class="tiles">
        <RouterLink class="card tile" :to="{ name: 'log', query: {} }">
          <span class="muted">{{ t('dashboard.sentToday') }}</span>
          <b class="mono">{{ (sum.today.sent ?? 0).toLocaleString(locale) }}</b>
        </RouterLink>
        <div class="card tile">
          <span class="muted">{{ t('dashboard.failedToday') }}</span>
          <b class="mono" :class="{ bad: sum.today.failed }">{{
            (sum.today.failed ?? 0).toLocaleString(locale)
          }}</b>
        </div>
        <div class="card tile">
          <span class="muted">{{ t('dashboard.inQueue') }}</span>
          <b class="mono">{{ sum.waiting.toLocaleString(locale) }}</b>
        </div>
        <div class="card tile">
          <span class="muted">{{ t('dashboard.held') }}</span>
          <b class="mono">{{ sum.held.toLocaleString(locale) }}</b>
        </div>
        <div class="card tile">
          <span class="muted">{{ t('dashboard.channelsOn') }}</span>
          <b class="mono">{{ sum.channels.toLocaleString(locale) }}</b>
        </div>
        <div class="card tile">
          <span class="muted">{{ t('dashboard.known') }}</span>
          <b class="mono">{{ sum.users.toLocaleString(locale) }}</b>
        </div>
      </div>
      <div v-if="sum.byChannel?.length" class="card">
        <b>{{ t('dashboard.byChannel') }}</b>
        <dl class="per">
          <template v-for="c in sum.byChannel" :key="c.channelId">
            <dt>{{ c.name || '#' + c.channelId }}</dt>
            <dd class="mono">{{ c.sent.toLocaleString(locale) }}</dd>
          </template>
        </dl>
      </div>
    </template>
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
.tiles {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(10rem, 1fr));
  gap: 0.75rem;
}
.tile {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
  color: inherit;
  text-decoration: none;
  padding: 1rem;
}
.tile b {
  font-size: 1.6rem;
}
.tile .bad {
  color: var(--p-red-600);
}
.per {
  margin-top: 0.6rem;
}
.scope {
  margin-inline-end: 0.35rem;
}
</style>
