<template>
  <v-card class="pa-4 mb-4">
    <div class="d-flex align-center flex-wrap ga-2 mb-2">
      <h2 ref="sectionHeading" tabindex="-1" class="text-subtitle-1 font-weight-bold">
        <v-icon start size="small">mdi-account-check-outline</v-icon>
        {{ $t('personal_zalo_identity_title') }}
      </h2>
      <v-spacer />
      <v-btn v-if="accountBound" size="small" variant="text" prepend-icon="mdi-refresh"
        :loading="loading" :disabled="issuing || revoking !== ''" @click="refresh">
        {{ $t('refresh') }}
      </v-btn>
    </div>
    <p class="text-body-2 text-medium-emphasis mb-4">{{ $t('personal_zalo_identity_description') }}</p>

    <v-alert v-if="!accountBound" type="info" variant="tonal" density="compact">
      {{ $t('personal_zalo_identity_connect_first') }}
    </v-alert>
    <template v-else>
      <v-alert v-if="!pairingEnabled && !loading" type="info" variant="tonal" density="compact" class="mb-4">
        {{ $t('personal_zalo_identity_prerequisites') }}
      </v-alert>
      <v-alert v-if="error" type="error" variant="tonal" density="compact" role="alert" class="mb-4">
        {{ $t(error) }}
      </v-alert>
      <form @submit.prevent="issue">
        <div class="d-flex flex-wrap ga-3 align-start">
          <v-select :model-value="kind" :items="kindOptions" :label="$t('personal_zalo_identity_kind')"
            density="compact" variant="outlined" hide-details :disabled="busy || !pairingEnabled"
            class="identity-field" @update:model-value="selectKind" />
          <v-autocomplete v-model="targetId" :items="targetOptions" :label="$t('personal_zalo_identity_person')"
            density="compact" variant="outlined" hide-details :disabled="busy || !pairingEnabled" :loading="loading"
            class="identity-field" />
          <v-btn type="submit" color="primary" :loading="issuing" :disabled="busy || !pairingEnabled || !targetId">
            {{ $t('personal_zalo_identity_issue') }}
          </v-btn>
        </div>
      </form>

      <v-sheet v-if="issued" border rounded="lg" class="pa-3 mt-4" aria-live="polite">
        <div class="d-flex align-start ga-2 mb-2">
          <p class="text-body-2 flex-grow-1">{{ $t('personal_zalo_identity_send', { name: issued.name }) }}</p>
          <v-btn icon="mdi-close" size="small" variant="text"
            :aria-label="$t('personal_zalo_identity_hide')" @click="issued = null" />
        </div>
        <div class="d-flex align-start flex-wrap ga-2">
          <v-text-field :model-value="issued.verification_command" :label="$t('personal_zalo_identity_command')"
            density="compact" variant="outlined" hide-details readonly class="identity-field" />
          <v-btn variant="tonal" prepend-icon="mdi-content-copy" @click="copyCommand">
            {{ $t(copied ? 'personal_zalo_identity_copied' : 'personal_zalo_identity_copy') }}
          </v-btn>
        </div>
        <p class="text-caption text-medium-emphasis mt-2">{{ expiry(issued.expires_at) }}</p>
      </v-sheet>

      <v-list v-if="links.length" density="compact" class="mt-3 pa-0">
        <v-list-item v-for="link in links" :key="link.id" class="px-0"
          :title="targetName(link.kind, link.target_id)"
          :subtitle="link.verified_at && link.sender_id ? `Zalo ID ${link.sender_id}` : expiry(link.expires_at)">
          <template #append>
            <v-chip size="small" variant="tonal" :color="link.verified_at ? 'success' : undefined">
              {{ $t(linkStatus(link)) }}
            </v-chip>
            <v-btn size="small" variant="text" color="error" class="ms-2" :disabled="busy"
              :aria-label="$t('personal_zalo_identity_revoke_name', { name: targetName(link.kind, link.target_id) })"
              @click="confirmRevoke(link, $event)">
              {{ $t('revoke') }}
            </v-btn>
          </template>
        </v-list-item>
      </v-list>

      <v-dialog :model-value="Boolean(pendingRevoke)" max-width="440" :aria-label="revokeTitle"
        :persistent="revoking !== ''" @update:model-value="dismissRevoke" @after-leave="restoreFocus">
        <v-card v-if="pendingRevoke" :title="revokeTitle">
          <v-card-text>{{ $t('personal_zalo_identity_revoke_confirm') }}</v-card-text>
          <v-card-actions>
            <v-spacer />
            <v-btn :disabled="revoking !== ''" @click="pendingRevoke = null">{{ $t('cancel') }}</v-btn>
            <v-btn color="error" :loading="revoking !== ''" @click="revoke">{{ $t('revoke') }}</v-btn>
          </v-card-actions>
        </v-card>
      </v-dialog>
    </template>
  </v-card>
</template>

<script setup lang="ts">
import { isAxiosError } from 'axios'
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'

type IdentityKind = 'staff' | 'customer'
type IdentityOption = { id: string; name: string; channel_name?: string; customer_code?: string }
type IdentityOptions = { staff: IdentityOption[]; customers: IdentityOption[]; pairing_enabled: boolean }
type IdentityLink = {
  id: string
  kind: IdentityKind
  target_id: string
  expires_at: string
  sender_id?: string
  verified_at?: string
}
type IssuedLink = IdentityLink & { verification_command: string; name: string }

const props = defineProps<{ tenantId: string; channelId: string; accountBound: boolean; channelAllowsPairing: boolean }>()
const { t, locale } = useI18n()
const endpoint = computed(() => `/tenants/${props.tenantId}/channels/${props.channelId}/personal-zalo/identity-links`)
const kind = ref<IdentityKind>('staff')
const targetId = ref<string | null>(null)
const options = ref<IdentityOptions>({ staff: [], customers: [], pairing_enabled: false })
const links = ref<IdentityLink[]>([])
const issued = ref<IssuedLink | null>(null)
const pendingRevoke = ref<IdentityLink | null>(null)
const loading = ref(false)
const issuing = ref(false)
const revoking = ref('')
const error = ref('')
const copied = ref(false)
const sectionHeading = ref<HTMLElement | null>(null)
let revokeActivator: HTMLElement | null = null

const busy = computed(() => loading.value || issuing.value || revoking.value !== '')
const pairingEnabled = computed(() => props.channelAllowsPairing && options.value.pairing_enabled)
const revokeTitle = computed(() => pendingRevoke.value
  ? t('personal_zalo_identity_revoke_name', { name: targetName(pendingRevoke.value.kind, pendingRevoke.value.target_id) })
  : undefined)
const kindOptions = computed(() => [
  { title: t('personal_zalo_identity_staff'), value: 'staff' },
  { title: t('personal_zalo_identity_customer'), value: 'customer' },
])
const targetOptions = computed(() => sourceOptions(kind.value).map((person) => ({
  title: [person.name, person.channel_name || person.customer_code].filter(Boolean).join(' · '),
  value: person.id,
})))

function sourceOptions(identityKind: IdentityKind) {
  return identityKind === 'staff' ? options.value.staff : options.value.customers
}

function targetName(identityKind: IdentityKind, id: string) {
  return sourceOptions(identityKind).find((person) => person.id === id)?.name || id
}

function linkStatus(link: IdentityLink) {
  if (link.verified_at) return 'personal_zalo_identity_verified'
  return Date.parse(link.expires_at) <= Date.now() ? 'personal_zalo_identity_expired' : 'personal_zalo_identity_pending'
}

function expiry(value: string) {
  return t('personal_zalo_identity_expiry', {
    time: new Intl.DateTimeFormat(locale.value, { dateStyle: 'short', timeStyle: 'short' }).format(new Date(value)),
  })
}

function selectKind(value: IdentityKind) {
  kind.value = value
  targetId.value = null
}

async function refresh() {
  if (!props.accountBound || loading.value) return
  loading.value = true
  error.value = ''
  const [linksResult, optionsResult] = await Promise.allSettled([
    api.get<IdentityLink[]>(endpoint.value),
    api.get<IdentityOptions>(`${endpoint.value}/options`),
  ])
  try {
    if (linksResult.status === 'fulfilled') {
      links.value = linksResult.value.data || []
    }
    if (optionsResult.status === 'fulfilled') {
      const nextOptions = optionsResult.value.data
      options.value = { ...nextOptions, staff: nextOptions.staff || [], customers: nextOptions.customers || [] }
    } else {
      options.value = { staff: [], customers: [], pairing_enabled: false }
    }
    if (issued.value && !links.value.some((link) => link.id === issued.value?.id && !link.verified_at && Date.parse(link.expires_at) > Date.now())) {
      issued.value = null
    }
    if (linksResult.status === 'rejected' || optionsResult.status === 'rejected') {
      error.value = 'personal_zalo_identity_load_error'
    }
  } catch {
    options.value = { staff: [], customers: [], pairing_enabled: false }
    error.value = 'personal_zalo_identity_load_error'
  } finally {
    loading.value = false
  }
}

async function issue() {
  if (!props.accountBound || !pairingEnabled.value || busy.value || !targetId.value) return
  issuing.value = true
  error.value = ''
  const name = targetName(kind.value, targetId.value)
  try {
    const { data } = await api.post<Omit<IssuedLink, 'name'>>(endpoint.value, { kind: kind.value, target_id: targetId.value })
    const { verification_command, ...link } = data
    issued.value = { ...link, verification_command, name }
    links.value = [link, ...links.value.filter((existing) => existing.kind !== link.kind || existing.target_id !== link.target_id)]
    targetId.value = null
    copied.value = false
  } catch (err) {
    const code = isAxiosError(err) ? err.response?.data?.error : ''
    if (code === 'personal_zalo_pairing_disabled') {
      options.value.pairing_enabled = false
      error.value = 'personal_zalo_identity_pairing_disabled'
    } else if (code === 'personal_zalo_account_not_bound') {
      error.value = 'personal_zalo_identity_connect_first'
    } else if (code === 'personal_zalo_identity_link_exists') {
      error.value = code
    } else {
      error.value = 'personal_zalo_identity_issue_error'
    }
  } finally {
    issuing.value = false
  }
}

async function copyCommand() {
  if (!issued.value) return
  if (Date.parse(issued.value.expires_at) <= Date.now()) {
    issued.value = null
    error.value = 'personal_zalo_identity_expired'
    return
  }
  try {
    await navigator.clipboard.writeText(issued.value.verification_command)
    copied.value = true
  } catch {
    error.value = 'personal_zalo_identity_copy_error'
  }
}

function dismissRevoke(open: boolean) {
  if (!open && !revoking.value) pendingRevoke.value = null
}

function confirmRevoke(link: IdentityLink, event: MouseEvent) {
  revokeActivator = event.currentTarget instanceof HTMLElement ? event.currentTarget : null
  pendingRevoke.value = link
}

function restoreFocus() {
  const target = revokeActivator?.isConnected ? revokeActivator : sectionHeading.value
  target?.focus({ preventScroll: true })
  revokeActivator = null
}

async function revoke() {
  if (busy.value || !pendingRevoke.value) return
  const linkId = pendingRevoke.value.id
  revoking.value = linkId
  error.value = ''
  try {
    await api.delete(`${endpoint.value}/${linkId}`)
    links.value = links.value.filter((link) => link.id !== linkId)
    if (issued.value?.id === linkId) issued.value = null
  } catch {
    error.value = 'personal_zalo_identity_revoke_error'
  } finally {
    pendingRevoke.value = null
    revoking.value = ''
  }
}

watch(() => props.channelAllowsPairing, refresh)
onMounted(refresh)
</script>

<style scoped>
.identity-field {
  flex: 1 1 14rem;
  min-width: 0;
}
</style>
