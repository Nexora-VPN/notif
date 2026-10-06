<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import IconField from 'primevue/iconfield'
import InputPassword from 'primevue/inputpassword'

// A secret typed into a form, with the show/hide control InputPassword (which
// replaced the deprecated Password) leaves to its caller — the panel's
// SecretInput. Every secret field of Notif is this one.
//
// `own` is Notif's own sign-in (the login, the admin's password): a password
// manager may fill and save it, under the autocomplete given. Any other
// secret — a bot token, a mail server's password — keeps the managers out:
// they would offer the admin's login there, so the field asks for a new
// password and carries the common managers' opt-outs.
const model = defineModel<string | null | undefined>()
const props = defineProps<{
  inputId?: string
  invalid?: boolean
  mono?: boolean
  placeholder?: string
  disabled?: boolean
  autocomplete?: string
  own?: boolean
}>()
const { t } = useI18n()
const masked = ref(true)
// A mono value (a token, a key) is typed left to right in every language:
// the whole field is, so its eye stays at the end the text leaves free.
const fill = computed(() =>
  props.own
    ? { autocomplete: props.autocomplete || 'current-password' }
    : {
        autocomplete: props.autocomplete || 'new-password',
        'data-1p-ignore': true,
        'data-lpignore': 'true',
        'data-form-type': 'other',
      },
)
</script>

<template>
  <IconField class="secret-input" :dir="mono ? 'ltr' : undefined">
    <InputPassword
      v-model="model"
      v-model:mask="masked"
      :id="inputId"
      :invalid="invalid"
      :disabled="disabled"
      :placeholder="placeholder"
      class="w-full"
      :class="{ mono }"
      fluid
      v-bind="fill"
    />
    <!-- A real button rather than InputIcon, which renders an aria-hidden
         span: a reveal control has to be reachable by keyboard and announced.
         p-inputicon is what IconField positions, on the trailing edge in both
         text directions. -->
    <button
      type="button"
      class="p-inputicon reveal-toggle"
      :aria-label="masked ? t('common.showPassword') : t('common.hidePassword')"
      :disabled="disabled"
      @click="masked = !masked"
      @keyup.stop
    >
      <i :class="masked ? 'pi pi-eye' : 'pi pi-eye-slash'" />
    </button>
  </IconField>
</template>

<style scoped>
.reveal-toggle {
  background: none;
  border: 0;
  padding: 0;
  color: inherit;
  cursor: pointer;
  line-height: 1;
}
.reveal-toggle:disabled {
  cursor: default;
  opacity: 0.5;
}
</style>
