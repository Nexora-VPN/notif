<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useToast } from 'primevue/usetoast'
import DataTable, { type DataTablePageEvent } from 'primevue/datatable'
import Column from 'primevue/column'
import InputText from 'primevue/inputtext'
import Button from 'primevue/button'
import Dialog from 'primevue/dialog'
import Tag from 'primevue/tag'
import { useRouter } from 'vue-router'
import { api, type BotLink, type Delivery, type NtfyFeed, type UserRow } from '../api'

const { t, te, locale } = useI18n()
const toast = useToast()

const q = ref('')
const items = ref<UserRow[]>([])
const total = ref(0)
const first = ref(0)
const rows = 25
const open = ref<{ user: UserRow; code: string; bots: BotLink[]; ntfy: NtfyFeed[] } | null>(null)
const busy = ref(false)
const router = useRouter()
const history = ref<Delivery[]>([])

const keys = ['telegram_id', 'bale_id', 'soroush_id', 'rubika_id', 'ntfy', 'phone', 'email']

function fail(e: unknown) {
  toast.add({
    severity: 'error',
    summary: t('common.error'),
    detail: (e as Error).message,
    life: 5000,
  })
}

async function load() {
  try {
    const r = await api.users(q.value.trim(), rows, first.value)
    items.value = r.items
    total.value = r.total
  } catch (e) {
    fail(e)
  }
}
onMounted(load)

function search() {
  first.value = 0
  void load()
}

function page(e: DataTablePageEvent) {
  first.value = e.first
  void load()
}

async function show(u: UserRow) {
  try {
    const r = await api.user(u.id)
    open.value = { user: r.user, code: r.code, bots: r.bots ?? [], ntfy: r.ntfy ?? [] }
    history.value = await api.history(u.id)
  } catch (e) {
    fail(e)
  }
}

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.add({ severity: 'success', summary: t('users.copied'), life: 2000 })
  } catch {
    // the text stays selectable
  }
}

async function unlink(key: string) {
  if (!open.value) return
  busy.value = true
  try {
    await api.unlink(open.value.user.id, key)
    await show(open.value.user)
    await load()
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

async function ntfyOn() {
  if (!open.value) return
  busy.value = true
  try {
    await api.ntfyOn(open.value.user.id)
    await show(open.value.user)
    await load()
  } catch (e) {
    fail(e)
  } finally {
    busy.value = false
  }
}

async function test() {
  if (!open.value) return
  try {
    await api.test(open.value.user.name)
    toast.add({ severity: 'success', summary: t('channels.testQueued'), life: 4000 })
  } catch (e) {
    fail(e)
  }
}

function when(unix: number) {
  return unix ? new Date(unix * 1000).toLocaleDateString(locale.value) : t('users.never')
}
</script>

<template>
  <section class="stack">
    <div class="head">
      <h2>{{ t('users.title') }}</h2>
      <form class="search" @submit.prevent="search">
        <InputText v-model="q" :placeholder="t('users.search')" dir="ltr" />
        <Button type="submit" icon="pi pi-search" :aria-label="t('users.search')" />
      </form>
    </div>
    <p class="muted">{{ t('users.intro') }}</p>
    <DataTable
      :value="items"
      data-key="id"
      lazy
      paginator
      :rows="rows"
      :first="first"
      :total-records="total"
      size="small"
      class="card table"
      selection-mode="single"
      @page="page"
      @row-click="(e) => show(e.data)"
    >
      <template #empty>{{ t('users.empty') }}</template>
      <Column :header="t('users.name')">
        <template #body="{ data }">
          <span class="mono" dir="ltr">{{ data.name }}</span>
        </template>
      </Column>
      <Column :header="t('users.reach')">
        <template #body="{ data }">
          <span class="tags">
            <Tag
              v-for="k in keys.filter((k) => data.reach[k])"
              :key="k"
              :value="t('users.keys.' + k)"
              severity="secondary"
            />
            <span v-if="!keys.some((k) => data.reach[k])" class="muted">{{
              t('users.nowhere')
            }}</span>
          </span>
        </template>
      </Column>
      <Column field="group" :header="t('users.group')" />
      <Column :header="t('users.expiry')">
        <template #body="{ data }">{{ when(data.expiry) }}</template>
      </Column>
    </DataTable>

    <Dialog
      :visible="!!open"
      modal
      :header="open?.user.name"
      class="dlg"
      @update:visible="(v: boolean) => !v && (open = null)"
    >
      <div v-if="open" class="stack">
        <div class="field">
          <label>{{ t('users.code') }}</label>
          <div class="row">
            <code class="mono code" dir="ltr">{{ open.code }}</code>
            <Button icon="pi pi-copy" text :aria-label="t('users.copy')" @click="copy(open.code)" />
          </div>
          <small class="muted">{{ t('users.codeHelp') }}</small>
        </div>
        <div v-for="b in open.bots" :key="b.channelId" class="bot">
          <div class="row">
            <b>{{ b.name }}</b>
            <span v-if="b.username" class="mono muted" dir="ltr">{{ '@' + b.username }}</span>
            <Tag
              :severity="b.linked ? 'success' : 'secondary'"
              :value="b.linked ? t('users.linked') : t('users.notLinked')"
            />
          </div>
          <div v-if="b.url" class="row">
            <a :href="b.url" target="_blank" rel="noopener" class="mono link" dir="ltr">{{
              b.url
            }}</a>
            <Button icon="pi pi-copy" text :aria-label="t('users.copy')" @click="copy(b.url)" />
          </div>
        </div>
        <div v-for="f in open.ntfy" :key="'n' + f.channelId" class="bot">
          <div class="row">
            <b>{{ f.name }}</b>
            <Tag
              :severity="f.topic ? 'success' : 'secondary'"
              :value="f.topic ? t('users.linked') : t('users.notLinked')"
            />
          </div>
          <div v-if="f.url" class="row">
            <code class="mono link" dir="ltr">{{ f.url }}</code>
            <Button icon="pi pi-copy" text :aria-label="t('users.copy')" @click="copy(f.url)" />
          </div>
          <small v-if="f.url" class="muted">{{ t('users.ntfyHelp') }}</small>
          <div v-else>
            <Button
              :label="t('users.ntfyOn')"
              size="small"
              outlined
              :loading="busy"
              @click="ntfyOn"
            />
          </div>
        </div>
        <div class="field">
          <label>{{ t('users.links') }}</label>
          <ul class="links">
            <li v-for="k in keys.filter((k) => open?.user.contact?.[k])" :key="k">
              <span>{{ t('users.keys.' + k) }}</span>
              <code class="mono" dir="ltr">{{ open.user.contact?.[k] }}</code>
              <Button
                v-if="k.endsWith('_id') || k === 'ntfy'"
                :label="t('users.unlink')"
                size="small"
                text
                severity="danger"
                :loading="busy"
                @click="unlink(k)"
              />
            </li>
          </ul>
          <span v-if="!keys.some((k) => open?.user.contact?.[k])" class="muted">{{
            t('users.nowhere')
          }}</span>
        </div>
        <div class="field">
          <label>{{ t('users.history') }}</label>
          <ul v-if="history.length" class="links">
            <li v-for="d in history" :key="d.id">
              <span class="mono muted">{{
                new Date(d.createdAt * 1000).toLocaleDateString(locale)
              }}</span>
              <span>{{
                te('notices.kinds.' + d.kind + '.name')
                  ? t('notices.kinds.' + d.kind + '.name')
                  : t('log.kinds.' + d.kind)
              }}</span>
              <Tag :value="t('log.states.' + d.status)" severity="secondary" />
              <span class="muted">{{ d.channelName }}</span>
            </li>
          </ul>
          <span v-else class="muted">{{ t('log.empty') }}</span>
        </div>
        <div class="end">
          <Button
            :label="t('users.message')"
            icon="pi pi-megaphone"
            outlined
            @click="
              router.push({ name: 'messages', query: { user: open.user.id, name: open.user.name } })
            "
          />
          <Button :label="t('channels.testSend')" icon="pi pi-send" @click="test" />
        </div>
      </div>
    </Dialog>
  </section>
</template>

<style scoped>
h2 {
  margin: 0;
}
p {
  margin: 0;
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
  flex-wrap: wrap;
}
.search {
  display: flex;
  gap: 0.4rem;
}
.table {
  padding: 0;
  overflow-x: auto;
}
.tags {
  display: inline-flex;
  gap: 0.3rem;
  flex-wrap: wrap;
}
.row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex-wrap: wrap;
}
.code {
  font-size: 1.2rem;
  user-select: all;
}
.bot {
  border-top: 1px solid var(--notif-border);
  padding-top: 0.75rem;
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
}
.link {
  overflow-wrap: anywhere;
  font-size: 0.85rem;
}
.links {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 0.3rem;
}
.links li {
  display: flex;
  gap: 0.6rem;
  align-items: center;
  flex-wrap: wrap;
}
.end {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  flex-wrap: wrap;
}
:deep(.dlg) {
  width: min(34rem, calc(100vw - 2rem));
}
</style>
