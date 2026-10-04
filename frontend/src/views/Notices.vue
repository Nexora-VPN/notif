<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import Textarea from 'primevue/textarea'
import Select from 'primevue/select'
import SelectButton from 'primevue/selectbutton'
import ToggleSwitch from 'primevue/toggleswitch'
import Tag from 'primevue/tag'
import { api, type Channel, type Notice, type NoticeText, type Schedule } from '../api'
import { locales } from '../i18n'

const { t, te } = useI18n()
const toast = useToast()

const list = ref<Notice[]>([])
const channels = ref<Channel[]>([])
const schedule = ref<Schedule>()
const days = ref('')
const percents = ref('')
const busy = ref(false)

const editing = ref<Notice | null>(null)
const draft = ref<Notice['texts']>({})
const lang = ref('fa')
const channelKind = ref('')
const preview = ref<NoticeText>()

const families = ['schedule', 'event', 'edit']

function fail(e: unknown) {
  toast.add({
    severity: 'error',
    summary: t('common.error'),
    detail: (e as Error).message,
    life: 6000,
  })
}

async function load() {
  try {
    ;[list.value, channels.value, schedule.value] = await Promise.all([
      api.notices(),
      api.channels(),
      api.schedule(),
    ])
    days.value = schedule.value.expiryDays.join(', ')
    percents.value = schedule.value.trafficPercents.join(', ')
  } catch (e) {
    fail(e)
  }
}
onMounted(load)

function name(kind: string) {
  return te('notices.kinds.' + kind + '.name') ? t('notices.kinds.' + kind + '.name') : kind
}
function about(kind: string) {
  return te('notices.kinds.' + kind + '.about') ? t('notices.kinds.' + kind + '.about') : ''
}

function numbers(s: string) {
  return s
    .split(/[,،\s]+/)
    .map((x) => x.replace(/[۰-۹]/g, (d) => String('۰۱۲۳۴۵۶۷۸۹'.indexOf(d))))
    .filter((x) => x !== '')
    .map(Number)
}

async function saveSchedule() {
  if (!schedule.value) return
  busy.value = true
  try {
    await api.saveSchedule({
      expiryDays: numbers(days.value),
      trafficPercents: numbers(percents.value),
      calendar: schedule.value.calendar,
    })
    toast.add({ severity: 'success', summary: t('common.saved'), life: 2500 })
    await load()
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

async function toggle(n: Notice, on: boolean) {
  try {
    await api.saveNotice(n.kind, { enabled: on, urgent: n.urgent, texts: n.texts })
    n.enabled = on
  } catch (e) {
    fail(e)
  }
}

// The channel kinds this install has, for a text of their own.
const kinds = computed(() => [...new Set(channels.value.map((c) => c.kind))])

function open(n: Notice) {
  editing.value = { ...n }
  draft.value = JSON.parse(JSON.stringify(n.texts ?? {}))
  channelKind.value = ''
  preview.value = undefined
}

// The text being edited: the admin's for this language and channel kind,
// shown over the default it replaces.
const current = computed<NoticeText>(() => {
  const per = draft.value[lang.value] ?? {}
  return per[channelKind.value] ?? { title: '', body: '' }
})
const fallback = computed<NoticeText>(() => {
  if (!editing.value) return { title: '', body: '' }
  const own = draft.value[lang.value]?.['']
  if (channelKind.value && own?.body) return own
  return editing.value.defaults[lang.value]
})

function set(field: 'title' | 'body', v: string) {
  const per = (draft.value[lang.value] ??= {})
  const t0 = (per[channelKind.value] ??= { title: '', body: '' })
  t0[field] = v
}

function insert(v: string) {
  set('body', (current.value.body || fallback.value.body) + ' {' + v + '}')
}

function restore() {
  const per = draft.value[lang.value]
  if (per) delete per[channelKind.value]
}

async function refresh() {
  if (!editing.value) return
  const title = current.value.title || fallback.value.title
  const body = current.value.body || fallback.value.body
  try {
    preview.value = await api.preview(editing.value.kind, lang.value, title, body)
  } catch (e) {
    fail(e)
  }
}
watch([lang, channelKind, () => current.value.body, () => current.value.title, editing], () => {
  void refresh()
})

async function save() {
  if (!editing.value) return
  busy.value = true
  try {
    await api.saveNotice(editing.value.kind, {
      enabled: editing.value.enabled,
      urgent: editing.value.urgent,
      texts: draft.value,
    })
    editing.value = null
    toast.add({ severity: 'success', summary: t('common.saved'), life: 2500 })
    await load()
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

function customised(n: Notice) {
  return Object.values(n.texts ?? {}).some((per) => Object.keys(per).length > 0)
}
</script>

<template>
  <section class="stack">
    <h2>{{ t('notices.title') }}</h2>
    <form v-if="schedule" class="card stack" @submit.prevent="saveSchedule">
      <h3>{{ t('notices.schedule') }}</h3>
      <div class="grid">
        <div class="field">
          <label for="sd">{{ t('notices.expiryDays') }}</label>
          <InputText id="sd" v-model="days" dir="ltr" placeholder="3, 1" />
          <small class="muted">{{ t('notices.expiryDaysHelp') }}</small>
        </div>
        <div class="field">
          <label for="sp">{{ t('notices.trafficPercents') }}</label>
          <InputText id="sp" v-model="percents" dir="ltr" placeholder="80, 95" />
          <small class="muted">{{ t('notices.trafficPercentsHelp') }}</small>
        </div>
        <div class="field">
          <label>{{ t('notices.calendar') }}</label>
          <Select
            v-model="schedule.calendar"
            :options="
              ['auto', 'jalali', 'gregorian'].map((c) => ({
                value: c,
                label: t('notices.calendars.' + c),
              }))
            "
            option-label="label"
            option-value="value"
          />
        </div>
      </div>
      <div>
        <Button type="submit" :label="t('common.save')" :loading="busy" />
      </div>
    </form>

    <div v-for="f in families" :key="f" class="stack">
      <h3>{{ t('notices.families.' + f) }}</h3>
      <ul class="list">
        <li v-for="n in list.filter((x) => x.family === f)" :key="n.kind" class="card row">
          <ToggleSwitch
            :model-value="n.enabled"
            :aria-label="name(n.kind)"
            @update:model-value="(v: boolean) => toggle(n, v)"
          />
          <div class="who">
            <b>{{ name(n.kind) }}</b>
            <small class="muted">{{ about(n.kind) }}</small>
          </div>
          <Tag v-if="n.urgent" :value="t('notices.urgent')" severity="warn" />
          <Tag v-if="customised(n)" :value="t('notices.customised')" severity="info" />
          <Button :label="t('notices.edit')" icon="pi pi-pencil" text @click="open(n)" />
        </li>
      </ul>
    </div>

    <Dialog
      :visible="!!editing"
      modal
      :header="editing ? name(editing.kind) : ''"
      class="dlg"
      @update:visible="(v: boolean) => !v && (editing = null)"
    >
      <div v-if="editing" class="stack">
        <div class="row">
          <label class="inline">
            <ToggleSwitch v-model="editing.enabled" />
            {{ t('notices.on') }}
          </label>
          <label class="inline">
            <ToggleSwitch v-model="editing.urgent" />
            {{ t('notices.urgentHelp') }}
          </label>
        </div>
        <SelectButton
          v-model="lang"
          :options="[...locales]"
          option-label="name"
          option-value="code"
          :allow-empty="false"
        />
        <div class="field">
          <label>{{ t('notices.forChannel') }}</label>
          <Select
            v-model="channelKind"
            :options="[
              { value: '', label: t('notices.allChannels') },
              ...kinds.map((k) => ({
                value: k,
                label: te('kinds.' + k + '.name') ? t('kinds.' + k + '.name') : k,
              })),
            ]"
            option-label="label"
            option-value="value"
          />
        </div>
        <div class="field">
          <label for="nt">{{ t('notices.textTitle') }}</label>
          <InputText
            id="nt"
            :model-value="current.title"
            :placeholder="fallback.title"
            :dir="lang === 'fa' ? 'rtl' : 'ltr'"
            @update:model-value="(v) => set('title', v ?? '')"
          />
        </div>
        <div class="field">
          <label for="nb">{{ t('notices.textBody') }}</label>
          <Textarea
            id="nb"
            :model-value="current.body"
            :placeholder="fallback.body"
            rows="4"
            auto-resize
            :dir="lang === 'fa' ? 'rtl' : 'ltr'"
            @update:model-value="(v) => set('body', v ?? '')"
          />
          <div class="chips">
            <Button
              v-for="v in editing.vars"
              :key="v"
              :label="'{' + v + '}'"
              size="small"
              severity="secondary"
              outlined
              class="mono"
              @click="insert(v)"
            />
          </div>
          <small class="muted">{{ t('notices.emptyIsDefault') }}</small>
        </div>
        <div v-if="preview" class="preview" :dir="lang === 'fa' ? 'rtl' : 'ltr'">
          <small class="muted">{{ t('notices.preview') }}</small>
          <b>{{ preview.title }}</b>
          <p>{{ preview.body }}</p>
        </div>
        <div class="end">
          <Button :label="t('notices.restore')" severity="secondary" text @click="restore" />
          <Button :label="t('common.save')" :loading="busy" @click="save" />
        </div>
      </div>
    </Dialog>
  </section>
</template>

<style scoped>
h2,
h3 {
  margin: 0;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr));
  gap: 1rem;
}
.list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.row {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  flex-wrap: wrap;
  padding: 0.75rem 1rem;
}
.who {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-width: 12rem;
}
.inline {
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
}
.chips {
  display: flex;
  flex-wrap: wrap;
  gap: 0.3rem;
}
.preview {
  background: var(--notif-bg);
  border: 1px solid var(--notif-border);
  border-radius: 10px;
  padding: 0.75rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.preview p {
  margin: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}
.end {
  display: flex;
  justify-content: space-between;
}
:deep(.dlg) {
  width: min(40rem, calc(100vw - 2rem));
}
</style>
