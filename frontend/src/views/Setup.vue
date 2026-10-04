<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Button from 'primevue/button'
import InputText from 'primevue/inputtext'
import Message from 'primevue/message'
import Tag from 'primevue/tag'
import { api, type Setup } from '../api'

// Set-up (GN-S7): every step a card, in order, each saying what to do next
// — the address and its certificate, checked from outside; the admin's own
// password and second factor; the first channel; the panel; the backups.
const { t, locale } = useI18n()
const board = ref<Setup>()
const error = ref('')
const address = ref('')
const checking = ref(false)
const checked = ref<{ reachable: boolean; error?: string }>()

// The address the placeholder shows.
const example = 'https://notif.example.com'

const left = computed(() => board.value?.steps.filter((s) => !s.done && !s.optional).length ?? 0)

async function load() {
  try {
    board.value = await api.setup()
    if (!address.value) address.value = board.value.publicUrl
  } catch (e) {
    error.value = (e as Error).message
  }
}

async function check() {
  checking.value = true
  checked.value = undefined
  try {
    checked.value = await api.setupAddress(address.value.trim(), true)
    await load()
  } catch (e) {
    checked.value = { reachable: false, error: (e as Error).message }
  } finally {
    checking.value = false
  }
}

function when(unix: string) {
  return new Date(Number(unix) * 1000).toLocaleString(locale.value)
}

onMounted(load)
</script>

<template>
  <section class="stack">
    <h2>{{ t('setup.title') }}</h2>
    <p class="muted">{{ t('setup.intro') }}</p>
    <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    <template v-if="board">
      <Message v-if="board.done" severity="success" :closable="false">{{
        t('setup.ready')
      }}</Message>
      <Message v-else severity="info" :closable="false">{{
        t('setup.left', { n: left.toLocaleString(locale) })
      }}</Message>

      <article
        v-for="(s, i) in board.steps"
        :key="s.key"
        class="card stack step"
        :class="{ done: s.done }"
      >
        <h3>
          <span class="num">{{ (i + 1).toLocaleString(locale) }}</span>
          {{ t('setup.steps.' + s.key) }}
          <Tag v-if="s.done" severity="success" :value="t('setup.done')" />
          <Tag v-else-if="s.optional" severity="secondary" :value="t('setup.optional')" />
        </h3>

        <template v-if="s.key === 'address'">
          <p class="muted">{{ t('setup.addressHint') }}</p>
          <div class="row">
            <InputText
              v-model="address"
              dir="ltr"
              :placeholder="example"
              class="grow"
              :aria-label="t('setup.steps.address')"
            />
            <Button :label="t('setup.check')" :loading="checking" @click="check" />
          </div>
          <Message v-if="checked?.reachable" severity="success" :closable="false">{{
            t('setup.reachable')
          }}</Message>
          <Message v-else-if="checked" severity="warn" :closable="false">
            {{ t('setup.unreachable') }}
            <span v-if="checked.error" dir="ltr" class="mono detail">{{ checked.error }}</span>
          </Message>
          <p v-if="board.adminUrl" class="muted">
            {{ t('setup.adminAt') }}
            <span dir="ltr" class="mono">{{ board.adminUrl }}</span>
          </p>
          <p v-if="board.basePath" class="muted">{{ t('setup.keepPath') }}</p>
          <p class="muted">{{ t('setup.https.' + board.https) }}</p>
          <p v-if="board.fingerprint" class="muted">
            {{ t('setup.fingerprint') }}:
            <span dir="ltr" class="mono fp">{{ board.fingerprint }}</span>
          </p>
        </template>

        <template v-if="s.key === 'password'">
          <p class="muted">{{ s.done ? t('setup.passwordDone') : t('setup.passwordTodo') }}</p>
          <RouterLink v-if="!s.done" :to="{ name: 'security' }">{{ t('nav.security') }}</RouterLink>
        </template>

        <template v-if="s.key === 'twofactor'">
          <p class="muted">{{ t('setup.twofactorHint') }}</p>
          <RouterLink v-if="!s.done" :to="{ name: 'security' }">{{ t('nav.security') }}</RouterLink>
        </template>

        <template v-if="s.key === 'channel'">
          <p class="muted">
            {{ s.done ? t('setup.channelsOn', { n: s.detail }) : t('setup.channelTodo') }}
          </p>
          <RouterLink :to="{ name: 'channels' }">{{ t('nav.channels') }}</RouterLink>
        </template>

        <template v-if="s.key === 'panel'">
          <template v-if="s.done">
            <p class="muted">{{ t('setup.panelDone') }}</p>
            <span dir="ltr" class="mono">{{ s.detail }}</span>
          </template>
          <template v-else>
            <p class="muted">{{ t('setup.panelTodo') }}</p>
            <div class="field">
              <label>{{ t('dashboard.claimCode') }}</label>
              <code class="mono">{{ s.detail }}</code>
            </div>
          </template>
        </template>

        <template v-if="s.key === 'backups'">
          <p v-if="board.database !== 'sqlite'" class="muted">{{ t('setup.backupsPostgres') }}</p>
          <p v-else-if="s.done" class="muted">
            {{ t('setup.backupsDone', { when: when(s.detail || '0') }) }}
          </p>
          <p v-else class="muted">{{ t('setup.backupsTodo') }}</p>
        </template>
      </article>
    </template>
  </section>
</template>

<style scoped>
h2,
h3 {
  margin: 0;
}
h3 {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  flex-wrap: wrap;
  font-size: 1.05rem;
}
.num {
  display: inline-grid;
  place-items: center;
  width: 1.6rem;
  height: 1.6rem;
  border-radius: 50%;
  border: 1px solid var(--notif-border);
  font-size: 0.85rem;
}
.step.done .num {
  border-color: transparent;
  background: var(--notif-border);
}
p {
  margin: 0;
}
.row {
  display: flex;
  gap: 0.75rem;
  flex-wrap: wrap;
}
.grow {
  flex: 1 1 18rem;
  min-width: 0;
}
.fp,
.detail {
  overflow-wrap: anywhere;
}
</style>
