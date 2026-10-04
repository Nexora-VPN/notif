<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import InputNumber from 'primevue/inputnumber'
import Textarea from 'primevue/textarea'
import SelectButton from 'primevue/selectbutton'
import MultiSelect from 'primevue/multiselect'
import ToggleSwitch from 'primevue/toggleswitch'
import Tag from 'primevue/tag'
import { api, type SendBody, type SendPlan, type SendReport, type UserRow } from '../api'

const { t, locale } = useI18n()
const route = useRoute()
const toast = useToast()
const confirm = useConfirm()

const mode = ref<'one' | 'group'>('group')
const picked = ref<UserRow[]>([])
const search = ref('')
const found = ref<UserRow[]>([])
const f = ref({
  group: '',
  status: [] as string[],
  q: '',
  expiringDays: null as number | null,
  usedGB: null as number | null,
  adminId: null as number | null,
  templateId: null as number | null,
  nodeId: null as number | null,
})
const title = ref('')
const body = ref('')
const urgent = ref(false)
const plan = ref<SendPlan>()
const busy = ref(false)
const sends = ref<SendReport[]>([])
let timer = 0

const vars = ['name', 'expiry', 'days', 'traffic_left', 'traffic_total', 'sub_url']
const statuses = ['active', 'disabled', 'expired', 'limited', 'pending']

function n(v: number) {
  return v.toLocaleString(locale.value)
}
function fail(e: unknown) {
  toast.add({
    severity: 'error',
    summary: t('common.error'),
    detail: (e as Error).message,
    life: 6000,
  })
}

// The request as the API takes it: the accounts picked, or the panel's
// filters the group's fields stand for.
const request = computed<SendBody>(() => {
  const b: SendBody = { title: title.value, body: body.value, urgent: urgent.value }
  if (mode.value === 'one') {
    b.userIds = picked.value.map((u) => u.id)
    return b
  }
  const x = f.value
  const filter: Record<string, string> = {}
  if (x.group.trim()) filter.group = x.group.trim()
  if (x.q.trim()) filter.q = x.q.trim()
  if (x.status.length) filter.status = x.status.join(',')
  if (x.expiringDays) {
    const now = Math.floor(Date.now() / 1000)
    filter.expires_after = String(now)
    filter.expires_before = String(now + x.expiringDays * 86400)
  }
  if (x.usedGB) filter.used_min = String(Math.round(x.usedGB * 1024 ** 3))
  if (x.adminId != null) filter.admin_id = String(x.adminId)
  if (x.templateId) filter.template_id = String(x.templateId)
  if (x.nodeId) filter.node_id = String(x.nodeId)
  b.filter = filter
  return b
})

watch(request, () => (plan.value = undefined), { deep: true })

async function find() {
  try {
    found.value = (await api.users(search.value.trim(), 10, 0)).items
  } catch (e) {
    fail(e)
  }
}
function pick(u: UserRow) {
  if (!picked.value.some((x) => x.id === u.id)) picked.value.push(u)
  found.value = []
  search.value = ''
}

const empty = computed(() =>
  mode.value === 'one'
    ? picked.value.length === 0
    : Object.keys(request.value.filter ?? {}).length === 0,
)

async function count() {
  busy.value = true
  try {
    plan.value = await api.planSend(request.value)
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

function send() {
  if (!plan.value) return
  const p = plan.value
  confirm.require({
    message: t('messages.confirm', { n: n(p.total - p.unreachable), sms: n(p.sms) }),
    acceptProps: { label: t('messages.send') },
    rejectProps: { label: t('common.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      busy.value = true
      try {
        await api.send(request.value)
        toast.add({ severity: 'success', summary: t('messages.queued'), life: 3000 })
        plan.value = undefined
        body.value = ''
        title.value = ''
        await loadSends()
      } catch (e) {
        fail(e)
      } finally {
        busy.value = false
      }
    },
  })
}

async function loadSends() {
  try {
    sends.value = await api.sends()
  } catch (e) {
    fail(e)
  }
}
async function cancel(r: SendReport) {
  try {
    await api.cancelSend(r.id)
    await loadSends()
  } catch (e) {
    fail(e)
  }
}

function insert(v: string) {
  body.value = (body.value ? body.value + ' ' : '') + '{' + v + '}'
}

// A send's recipients as the admin chose them, in their words.
const filterWords: Record<string, string> = {
  group: 'group',
  status: 'status',
  q: 'search',
  admin_id: 'owner',
  template_id: 'template',
  node_id: 'node',
  expires_before: 'expiringUntil',
  used_min: 'usedGB',
}
function who(r: SendReport) {
  return Object.entries(r.filter ?? {})
    .filter(([k]) => k !== 'expires_after')
    .map(([k, v]) => {
      if (k === 'accounts') return v
      let value = v
      if (k === 'expires_before')
        value = new Date(Number(v) * 1000).toLocaleDateString(locale.value)
      if (k === 'used_min') value = n(Math.round((Number(v) / 1024 ** 3) * 10) / 10)
      if (k === 'status')
        value = v
          .split(',')
          .map((x) => t('messages.statuses.' + x))
          .join('، ')
      return (filterWords[k] ? t('messages.' + filterWords[k]) : k) + ': ' + value
    })
    .join(' · ')
}
function when(unix: number) {
  return new Date(unix * 1000).toLocaleString(locale.value)
}

function poll() {
  timer = window.setTimeout(async () => {
    await loadSends()
    poll()
  }, 4000)
}

onMounted(async () => {
  const id = Number(route.query.user)
  if (id) {
    mode.value = 'one'
    picked.value = [{ id, name: String(route.query.name ?? '#' + id) } as UserRow]
  }
  await loadSends()
  poll()
})
onBeforeUnmount(() => window.clearTimeout(timer))
</script>

<template>
  <section class="stack">
    <h2>{{ t('messages.title') }}</h2>
    <div class="card stack">
      <SelectButton
        v-model="mode"
        :options="[
          { value: 'one', label: t('messages.toAccounts') },
          { value: 'group', label: t('messages.toGroup') },
        ]"
        option-label="label"
        option-value="value"
        :allow-empty="false"
      />
      <template v-if="mode === 'one'">
        <form class="row" @submit.prevent="find">
          <InputText v-model="search" :placeholder="t('users.search')" dir="ltr" />
          <Button type="submit" icon="pi pi-search" :aria-label="t('users.search')" />
        </form>
        <div v-if="found.length" class="tags">
          <Button
            v-for="u in found"
            :key="u.id"
            :label="u.name"
            size="small"
            outlined
            class="mono"
            @click="pick(u)"
          />
        </div>
        <div class="tags">
          <Tag v-for="u in picked" :key="u.id" severity="secondary">
            <span class="mono" dir="ltr">{{ u.name }}</span>
            <i class="pi pi-times x" @click="picked = picked.filter((p) => p.id !== u.id)" />
          </Tag>
        </div>
      </template>
      <div v-else class="grid">
        <div class="field">
          <label for="fg">{{ t('messages.group') }}</label>
          <InputText id="fg" v-model="f.group" dir="ltr" />
        </div>
        <div class="field">
          <label>{{ t('messages.status') }}</label>
          <MultiSelect
            v-model="f.status"
            :options="statuses.map((s) => ({ value: s, label: t('messages.statuses.' + s) }))"
            option-label="label"
            option-value="value"
            display="chip"
          />
        </div>
        <div class="field">
          <label for="fe">{{ t('messages.expiring') }}</label>
          <InputNumber v-model="f.expiringDays" input-id="fe" :min="1" :max="3650" />
        </div>
        <div class="field">
          <label for="fu">{{ t('messages.usedGB') }}</label>
          <InputNumber v-model="f.usedGB" input-id="fu" :min="0" :max-fraction-digits="1" />
        </div>
        <div class="field">
          <label for="fq">{{ t('messages.search') }}</label>
          <InputText id="fq" v-model="f.q" dir="ltr" />
        </div>
        <div class="field">
          <label for="fa">{{ t('messages.owner') }}</label>
          <InputNumber v-model="f.adminId" input-id="fa" :min="0" :use-grouping="false" />
        </div>
        <div class="field">
          <label for="ft">{{ t('messages.template') }}</label>
          <InputNumber v-model="f.templateId" input-id="ft" :min="1" :use-grouping="false" />
        </div>
        <div class="field">
          <label for="fn">{{ t('messages.node') }}</label>
          <InputNumber v-model="f.nodeId" input-id="fn" :min="1" :use-grouping="false" />
        </div>
      </div>
      <div class="field">
        <label for="mt">{{ t('notices.textTitle') }}</label>
        <InputText id="mt" v-model="title" maxlength="200" />
      </div>
      <div class="field">
        <label for="mb">{{ t('notices.textBody') }}</label>
        <Textarea id="mb" v-model="body" rows="4" auto-resize maxlength="4000" />
        <div class="tags">
          <Button
            v-for="v in vars"
            :key="v"
            :label="'{' + v + '}'"
            size="small"
            severity="secondary"
            outlined
            class="mono"
            @click="insert(v)"
          />
        </div>
      </div>
      <label class="inline">
        <ToggleSwitch v-model="urgent" />
        {{ t('notices.urgentHelp') }}
      </label>
      <div class="row">
        <Button
          :label="t('messages.count')"
          icon="pi pi-calculator"
          outlined
          :loading="busy"
          :disabled="empty"
          @click="count"
        />
        <Button
          :label="t('messages.send')"
          icon="pi pi-send"
          :disabled="!plan || !body.trim() || plan.total - plan.unreachable === 0"
          :loading="busy"
          @click="send"
        />
      </div>
      <div v-if="plan" class="plan">
        <b>{{ t('messages.planTotal', { n: n(plan.total) }) }}</b>
        <ul>
          <li v-for="c in plan.byChannel ?? []" :key="c.channelId">
            {{ c.name }}: <span class="mono">{{ n(c.count) }}</span>
            <Tag v-if="c.sms" value="SMS" severity="warn" />
          </li>
          <li v-if="plan.unreachable" class="muted">
            {{ t('messages.unreachable') }}: <span class="mono">{{ n(plan.unreachable) }}</span>
          </li>
        </ul>
        <b>{{ t('messages.planSMS', { n: n(plan.sms) }) }}</b>
        <small v-if="plan.sample?.length" class="muted mono" dir="ltr">
          {{ plan.sample.join(', ') }}{{ plan.total > plan.sample.length ? ', …' : '' }}
        </small>
        <div v-if="plan.preview" class="preview">
          <small class="muted">{{ t('notices.preview') }}</small>
          <b v-if="plan.preview.title">{{ plan.preview.title }}</b>
          <p>{{ plan.preview.body }}</p>
        </div>
        <small class="muted">{{ t('messages.planNote') }}</small>
      </div>
    </div>

    <h3>{{ t('messages.sent') }}</h3>
    <p v-if="!sends.length" class="muted">{{ t('log.empty') }}</p>
    <article v-for="r in sends" :key="r.id" class="card stack small">
      <div class="row between">
        <b>{{ r.title || r.body.slice(0, 60) }}</b>
        <span class="muted mono">{{ when(r.createdAt) }}</span>
      </div>
      <small class="muted who">{{ who(r) }}</small>
      <div class="row">
        <Tag severity="success" :value="t('log.states.sent') + ' ' + n(r.sent)" />
        <Tag
          v-if="r.queued"
          severity="secondary"
          :value="t('log.states.queued') + ' ' + n(r.queued)"
        />
        <Tag
          v-if="r.failed"
          severity="danger"
          :value="t('log.states.failed') + ' ' + n(r.failed)"
        />
        <Tag
          v-if="r.cancelled"
          severity="secondary"
          :value="t('log.states.cancelled') + ' ' + n(r.cancelled)"
        />
        <Tag
          v-if="r.unreachable"
          severity="warn"
          :value="t('messages.unreachable') + ' ' + n(r.unreachable)"
        />
        <span v-for="c in r.byChannel ?? []" :key="c.channelId" class="muted">
          {{ c.name }} <span class="mono">{{ n(c.count) }}</span>
        </span>
        <Button
          v-if="r.queued"
          :label="t('log.cancel')"
          size="small"
          text
          severity="danger"
          @click="cancel(r)"
        />
      </div>
    </article>
  </section>
</template>

<style scoped>
h2,
h3 {
  margin: 0;
}
p {
  margin: 0;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(13rem, 1fr));
  gap: 1rem;
}
.row {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  flex-wrap: wrap;
}
.between {
  justify-content: space-between;
}
.tags {
  display: flex;
  gap: 0.35rem;
  flex-wrap: wrap;
}
.x {
  cursor: pointer;
  margin-inline-start: 0.4rem;
  font-size: 0.7rem;
}
.inline {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
}
.plan {
  border: 1px solid var(--notif-border);
  border-radius: 10px;
  padding: 0.75rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.plan ul {
  margin: 0;
  padding-inline-start: 1.2rem;
}
.preview {
  background: var(--notif-bg);
  border-radius: 8px;
  padding: 0.6rem 0.8rem;
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}
.preview p {
  margin: 0.2rem 0 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.small {
  padding: 0.9rem 1rem;
  gap: 0.4rem;
}
.who {
  overflow-wrap: anywhere;
}
</style>
