<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'
import { useToast } from 'primevue/usetoast'
import { useConfirm } from 'primevue/useconfirm'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import InputText from 'primevue/inputtext'
import InputNumber from 'primevue/inputnumber'
import Textarea from 'primevue/textarea'
import Select from 'primevue/select'
import ToggleSwitch from 'primevue/toggleswitch'
import Tag from 'primevue/tag'
import { api, type Channel, type ChannelBody, type Kind } from '../api'

const { t, te, locale } = useI18n()
const toast = useToast()
const confirm = useConfirm()
const router = useRouter()

const kinds = ref<Kind[]>([])
const channels = ref<Channel[]>([])
const editing = ref<Channel | null>(null)
const open = ref(false)
const form = ref<ChannelBody & { kind: string }>({
  kind: 'http',
  name: '',
  enabled: true,
  perMinute: 0,
  config: {},
})
const busy = ref(false)
const testing = ref<Channel | null>(null)
const preset = ref('')
const testUser = ref('')
const testQuiet = ref(false)

const kind = computed(() => kinds.value.find((k) => k.name === form.value.kind))

function rate(c: Channel) {
  return (c.perMinute || kinds.value.find((k) => k.name === c.kind)?.perMinute || 0).toLocaleString(
    locale.value,
  )
}

function kindName(name: string) {
  return te('kinds.' + name + '.name') ? t('kinds.' + name + '.name') : name
}
// A field's words: the kind's own, else the ones the bots share.
function fieldText(k: string, f: string, fallback: string) {
  if (te('kinds.' + k + '.' + f)) return t('kinds.' + k + '.' + f)
  if (te('fields.' + f)) return t('fields.' + f)
  return fallback
}
function fieldLabel(k: string, f: string) {
  return fieldText(k, f, f)
}
function fieldHelp(k: string, f: string) {
  return fieldText(k, f + 'Help', '')
}

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
    ;[kinds.value, channels.value] = await Promise.all([api.kinds(), api.channels()])
  } catch (e) {
    fail(e)
  }
}
onMounted(load)

function startAdd() {
  editing.value = null
  preset.value = ''
  const k = kinds.value[0]
  form.value = { kind: k?.name ?? 'http', name: '', enabled: true, perMinute: 0, config: {} }
  for (const f of k?.fields ?? []) if (f.default) form.value.config[f.key] = f.default
  open.value = true
}

function pickKind(name: string) {
  form.value.kind = name
  form.value.config = {}
  preset.value = ''
  for (const f of kind.value?.fields ?? []) if (f.default) form.value.config[f.key] = f.default
}

// A preset fills the kind's fields; the admin's own values are left in
// capitals for them to replace.
function pickPreset(name: string) {
  preset.value = name
  const p = kind.value?.presets?.find((x) => x.name === name)
  if (!p) return
  const config: Record<string, string> = {}
  for (const f of kind.value?.fields ?? []) config[f.key] = p.config[f.key] ?? ''
  form.value.config = config
  if (!form.value.name) form.value.name = p.name
}

function startEdit(c: Channel) {
  editing.value = c
  form.value = {
    kind: c.kind,
    name: c.name,
    enabled: c.enabled,
    perMinute: c.perMinute,
    config: { ...c.config },
  }
  open.value = true
}

async function save() {
  busy.value = true
  try {
    if (editing.value) await api.updateChannel(editing.value.id, form.value)
    else await api.createChannel(form.value)
    open.value = false
    toast.add({ severity: 'success', summary: t('common.saved'), life: 2500 })
    await load()
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

async function toggle(c: Channel, on: boolean) {
  try {
    await api.updateChannel(c.id, {
      name: c.name,
      enabled: on,
      perMinute: c.perMinute,
      config: c.config,
    })
    await load()
  } catch (e) {
    fail(e)
  }
}

async function move(i: number, by: number) {
  const ids = channels.value.map((c) => c.id)
  const j = i + by
  if (j < 0 || j >= ids.length) return
  ;[ids[i], ids[j]] = [ids[j], ids[i]]
  try {
    await api.orderChannels(ids)
    await load()
  } catch (e) {
    fail(e)
  }
}

function remove(c: Channel) {
  confirm.require({
    message: t('channels.deleteConfirm', { name: c.name }),
    acceptProps: { label: t('common.delete'), severity: 'danger' },
    rejectProps: { label: t('common.cancel'), severity: 'secondary', outlined: true },
    accept: async () => {
      try {
        await api.deleteChannel(c.id)
        await load()
      } catch (e) {
        fail(e)
      }
    },
  })
}

function startTest(c: Channel) {
  testing.value = c
}

async function sendTest() {
  if (!testing.value) return
  busy.value = true
  try {
    await api.test(testUser.value.trim(), testing.value.id, testQuiet.value)
    testing.value = null
    toast.add({ severity: 'success', summary: t('channels.testQueued'), life: 4000 })
    await router.push({ name: 'log' })
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

const variables =
  '{{.Text}}  {{.Title}}  {{.Address}}  {{.Name}}  {{.Secret}}  {{.ID}}  {{.Key}}  {{.Vars.name}}\n' +
  '{{json .Text}}  {{urlquery .Text}}  {{e164 .Address}}  {{msisdn .Address}}  {{iran .Address}}'
</script>

<template>
  <section class="stack">
    <div class="head">
      <h2>{{ t('channels.title') }}</h2>
      <Button :label="t('channels.add')" icon="pi pi-plus" @click="startAdd" />
    </div>
    <p class="muted">{{ t('channels.intro') }}</p>
    <p v-if="!channels.length" class="card muted">{{ t('channels.empty') }}</p>
    <ol class="list">
      <li v-for="(c, i) in channels" :key="c.id" class="card row" :class="{ off: !c.enabled }">
        <span class="pos mono">{{ i + 1 }}</span>
        <div class="who">
          <b>{{ c.name }}</b>
          <Tag :value="kindName(c.kind)" severity="secondary" />
          <span v-if="c.state?.username" class="mono muted" dir="ltr">{{
            '@' + c.state.username
          }}</span>
          <Tag
            v-if="c.state?.error"
            severity="danger"
            :value="t('channels.problem')"
            v-tooltip="c.state.error"
          />
          <small class="muted">
            {{
              t('channels.sent', {
                today: c.sentToday.toLocaleString(locale),
                month: c.sentMonth.toLocaleString(locale),
              })
            }}
          </small>
          <small class="muted">
            {{ t('channels.perMinute') }}:
            <span class="mono">{{ rate(c) }}</span>
          </small>
        </div>
        <p v-if="c.state?.error" class="err">{{ c.state.error }}</p>
        <div class="acts">
          <ToggleSwitch
            :model-value="c.enabled"
            :aria-label="t('channels.enabled')"
            @update:model-value="(v: boolean) => toggle(c, v)"
          />
          <Button
            icon="pi pi-arrow-up"
            text
            severity="secondary"
            :disabled="i === 0"
            :aria-label="t('channels.up')"
            @click="move(i, -1)"
          />
          <Button
            icon="pi pi-arrow-down"
            text
            severity="secondary"
            :disabled="i === channels.length - 1"
            :aria-label="t('channels.down')"
            @click="move(i, 1)"
          />
          <Button :label="t('channels.test')" icon="pi pi-send" text @click="startTest(c)" />
          <Button icon="pi pi-pencil" text :aria-label="t('common.edit')" @click="startEdit(c)" />
          <Button
            icon="pi pi-trash"
            text
            severity="danger"
            :aria-label="t('common.delete')"
            @click="remove(c)"
          />
        </div>
      </li>
    </ol>

    <Dialog
      v-model:visible="open"
      modal
      :header="editing ? t('channels.edit') : t('channels.add')"
      class="dlg"
    >
      <form class="stack" @submit.prevent="save">
        <div class="field">
          <label>{{ t('channels.kind') }}</label>
          <Select
            :model-value="form.kind"
            :options="kinds.map((k) => ({ value: k.name, label: kindName(k.name) }))"
            option-label="label"
            option-value="value"
            :disabled="!!editing"
            @update:model-value="pickKind"
          />
        </div>
        <div v-if="!editing && kind?.presets?.length" class="field">
          <label>{{ t('channels.preset') }}</label>
          <Select
            :model-value="preset"
            :options="kind.presets.map((p) => ({ value: p.name, label: p.name }))"
            option-label="label"
            option-value="value"
            :placeholder="t('channels.presetNone')"
            @update:model-value="pickPreset"
          />
          <small class="muted">{{ t('channels.presetHelp') }}</small>
        </div>
        <div class="field">
          <label for="cn">{{ t('channels.name') }}</label>
          <InputText id="cn" v-model="form.name" maxlength="64" />
        </div>
        <div class="row2">
          <div class="field">
            <label for="pm">{{ t('channels.perMinute') }}</label>
            <InputNumber v-model="form.perMinute" input-id="pm" :min="0" :max="100000" />
            <small class="muted">{{
              t('channels.perMinuteHint', { n: (kind?.perMinute ?? 0).toLocaleString(locale) })
            }}</small>
          </div>
          <div class="field">
            <label>{{ t('channels.enabled') }}</label>
            <ToggleSwitch v-model="form.enabled" />
          </div>
        </div>
        <div v-for="f in kind?.fields ?? []" :key="f.key" class="field">
          <label :for="'f-' + f.key">{{ fieldLabel(form.kind, f.key) }}</label>
          <Select
            v-if="f.choices?.length"
            :id="'f-' + f.key"
            v-model="form.config[f.key]"
            :options="
              f.choices.map((c) => ({ value: c, label: fieldText(form.kind, f.key + '_' + c, c) }))
            "
            option-label="label"
            option-value="value"
          />
          <Textarea
            v-else-if="f.multiline"
            :id="'f-' + f.key"
            v-model="form.config[f.key]"
            rows="4"
            class="mono"
            dir="ltr"
            auto-resize
          />
          <InputText
            v-else
            :id="'f-' + f.key"
            v-model="form.config[f.key]"
            class="mono"
            dir="ltr"
          />
          <small v-if="fieldHelp(form.kind, f.key)" class="muted">{{
            fieldHelp(form.kind, f.key)
          }}</small>
          <small v-if="f.secret && editing" class="muted">{{ t('channels.secretKept') }}</small>
        </div>
        <div v-if="form.kind === 'kavenegar' || form.kind === 'faraz'" class="field">
          <label>{{ t('channels.noticeVars') }}</label>
          <code class="mono vars" dir="ltr">{name} {days} {expiry} {traffic} {title} {text}</code>
        </div>
        <div v-if="form.kind === 'http'" class="field">
          <label>{{ t('channels.variables') }}</label>
          <code class="mono vars" dir="ltr">{{ variables }}</code>
        </div>
        <div class="end">
          <Button
            type="button"
            :label="t('common.cancel')"
            severity="secondary"
            text
            @click="open = false"
          />
          <Button type="submit" :label="t('common.save')" :loading="busy" :disabled="!form.name" />
        </div>
      </form>
    </Dialog>

    <Dialog
      :visible="!!testing"
      modal
      :header="t('channels.testSend') + (testing ? ' · ' + testing.name : '')"
      class="dlg"
      @update:visible="(v: boolean) => !v && (testing = null)"
    >
      <form class="stack" @submit.prevent="sendTest">
        <div class="field">
          <label for="tu">{{ t('channels.testUser') }}</label>
          <InputText id="tu" v-model="testUser" dir="ltr" autofocus />
        </div>
        <label class="inline">
          <ToggleSwitch v-model="testQuiet" />
          {{ t('channels.testQuiet') }}
        </label>
        <div class="end">
          <Button
            type="submit"
            :label="t('channels.testSend')"
            :loading="busy"
            :disabled="!testUser.trim()"
          />
        </div>
      </form>
    </Dialog>
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
p {
  margin: 0;
}
.list {
  list-style: none;
  padding: 0;
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 0.6rem;
}
.row {
  display: flex;
  align-items: center;
  gap: 1rem;
  flex-wrap: wrap;
  padding: 0.75rem 1rem;
}
.err {
  flex-basis: 100%;
  order: 3;
  margin: 0;
  font-size: 0.85rem;
  color: var(--p-red-600);
  overflow-wrap: anywhere;
}
.off {
  opacity: 0.6;
}
.pos {
  font-size: 1.2rem;
  color: var(--p-primary-600);
  min-width: 1.5rem;
}
.who {
  display: flex;
  gap: 0.6rem;
  align-items: center;
  flex-wrap: wrap;
  flex: 1;
  min-width: 10rem;
}
.acts {
  display: flex;
  align-items: center;
  gap: 0.15rem;
  flex-wrap: wrap;
}
.row2 {
  display: grid;
  grid-template-columns: 1fr auto;
  gap: 1rem;
}
.end {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
}
.inline {
  display: inline-flex;
  align-items: center;
  gap: 0.6rem;
}
.vars {
  font-size: 0.8rem;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}
:deep(.dlg) {
  width: min(36rem, calc(100vw - 2rem));
}
</style>
