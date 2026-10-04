<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable, { type DataTablePageEvent } from 'primevue/datatable'
import Column from 'primevue/column'
import Tag from 'primevue/tag'
import Select from 'primevue/select'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import { api, type Delivery } from '../api'

const { t, te, locale } = useI18n()
const toast = useToast()

const items = ref<Delivery[]>([])
const total = ref(0)
const first = ref(0)
const rows = 25
const status = ref('')
const user = ref('')
const expanded = ref<Record<number, boolean>>({})
const details = ref<Record<number, Delivery>>({})
let timer = 0

const states = ['queued', 'held', 'sending', 'sent', 'failed', 'cancelled', 'unknown']

function severity(s: string) {
  return (
    {
      sent: 'success',
      failed: 'danger',
      unknown: 'warn',
      held: 'info',
      queued: 'secondary',
      sending: 'secondary',
      cancelled: 'secondary',
    } as Record<string, string>
  )[s]
}

function when(unix: number) {
  return unix ? new Date(unix * 1000).toLocaleString(locale.value) : '—'
}
function noticeName(k: string) {
  if (te('notices.kinds.' + k + '.name')) return t('notices.kinds.' + k + '.name')
  return te('log.kinds.' + k) ? t('log.kinds.' + k) : k
}

async function load() {
  try {
    const r = await api.deliveries({
      status: status.value,
      user: user.value.trim(),
      limit: rows,
      offset: first.value,
    })
    items.value = r.items
    total.value = r.total
    const shown = Object.keys(expanded.value).map(Number)
    const got = await Promise.all(shown.map((id) => api.delivery(id)))
    got.forEach((d) => (details.value[d.id] = d))
  } catch (e) {
    toast.add({
      severity: 'error',
      summary: t('common.error'),
      detail: (e as Error).message,
      life: 5000,
    })
  }
}

function page(e: DataTablePageEvent) {
  first.value = e.first
  void load()
}

async function open(e: { data: Delivery }) {
  details.value[e.data.id] = await api.delivery(e.data.id)
}

async function cancel(d: Delivery) {
  try {
    await api.cancelDelivery(d.id)
    await load()
  } catch (e) {
    toast.add({
      severity: 'error',
      summary: t('common.error'),
      detail: (e as Error).message,
      life: 5000,
    })
  }
}

watch(status, () => {
  first.value = 0
  void load()
})

function poll() {
  timer = window.setTimeout(async () => {
    await load()
    poll()
  }, 5000)
}

onMounted(async () => {
  await load()
  poll()
})
onBeforeUnmount(() => window.clearTimeout(timer))
</script>

<template>
  <section class="stack">
    <div class="head">
      <h2>{{ t('log.title') }}</h2>
      <div class="filters">
        <Select
          v-model="status"
          :options="[
            { value: '', label: t('log.all') },
            ...states.map((s) => ({ value: s, label: t('log.states.' + s) })),
          ]"
          option-label="label"
          option-value="value"
        />
        <InputText
          v-model="user"
          :placeholder="t('log.filterUser')"
          dir="ltr"
          @keyup.enter="
            () => {
              first = 0
              load()
            }
          "
        />
        <Button icon="pi pi-refresh" text :aria-label="t('common.refresh')" @click="load" />
      </div>
    </div>
    <DataTable
      v-model:expanded-rows="expanded"
      :value="items"
      data-key="id"
      lazy
      paginator
      :rows="rows"
      :first="first"
      :total-records="total"
      size="small"
      class="card table"
      @page="page"
      @row-expand="open"
    >
      <template #empty>{{ t('log.empty') }}</template>
      <Column expander style="width: 3rem" />
      <Column :header="t('log.time')">
        <template #body="{ data }">
          <span class="mono nowrap">{{ when(data.createdAt) }}</span>
        </template>
      </Column>
      <Column :header="t('log.status')">
        <template #body="{ data }">
          <Tag :value="t('log.states.' + data.status)" :severity="severity(data.status)" />
        </template>
      </Column>
      <Column field="userName" :header="t('log.user')">
        <template #body="{ data }">
          <span class="mono" dir="ltr">{{ data.userName || '#' + data.userId }}</span>
        </template>
      </Column>
      <Column :header="t('log.notice')">
        <template #body="{ data }">
          {{ noticeName(data.kind) }}
          <Tag v-if="data.urgent" :value="t('log.urgent')" severity="warn" class="small" />
        </template>
      </Column>

      <Column field="channelName" :header="t('log.channel')" />
      <Column :header="t('log.error')">
        <template #body="{ data }">
          <span class="err">{{ data.error }}</span>
          <span v-if="data.status === 'queued' || data.status === 'held'" class="muted nowrap">
            {{ t('log.nextAt') }}: {{ when(data.nextAt) }}
          </span>
        </template>
      </Column>
      <Column style="width: 6rem">
        <template #body="{ data }">
          <Button
            v-if="data.status === 'queued' || data.status === 'held'"
            :label="t('log.cancel')"
            size="small"
            text
            severity="danger"
            @click="cancel(data)"
          />
        </template>
      </Column>
      <template #expansion="{ data }">
        <div class="attempts">
          <b>{{ t('log.attempts') }}</b>
          <ol v-if="details[data.id]?.attempts?.length">
            <li v-for="a in details[data.id].attempts" :key="a.id">
              <span class="mono">{{ when(a.at) }}</span>
              · {{ a.channelName || '#' + a.channelId }} ·
              <b>{{ t('log.outcomes.' + a.outcome) }}</b>
              <span v-if="a.detail" class="muted mono detail" dir="ltr">{{ a.detail }}</span>
            </li>
          </ol>
          <p v-else class="muted">—</p>
        </div>
      </template>
    </DataTable>
  </section>
</template>

<style scoped>
h2 {
  margin: 0;
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
  flex-wrap: wrap;
}
.filters {
  display: flex;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.table {
  padding: 0;
  overflow-x: auto;
}
.nowrap {
  white-space: nowrap;
}
.err {
  overflow-wrap: anywhere;
}
.small {
  margin-inline-start: 0.35rem;
}
.attempts ol {
  margin: 0.4rem 0 0;
  padding-inline-start: 1.25rem;
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.detail {
  display: block;
  font-size: 0.8rem;
  overflow-wrap: anywhere;
}
</style>
