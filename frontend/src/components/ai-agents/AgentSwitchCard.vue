<template>
  <v-card class="pa-4 pa-sm-5 mb-4" data-testid="agent-switch-card">
    <div class="d-flex align-start flex-wrap ga-3">
      <v-avatar :color="settings.enabled ? 'success' : 'grey'" variant="tonal" size="44">
        <v-icon>{{ settings.enabled ? 'mdi-account-network' : 'mdi-account-network-outline' }}</v-icon>
      </v-avatar>
      <div style="flex: 1 1 280px; min-width: 0">
        <div class="text-subtitle-1 font-weight-bold">{{ $t('ag_switch_title') }}</div>
        <div class="text-body-2 text-medium-emphasis" data-testid="agent-switch-status">
          {{ settings.enabled ? $t('ag_switch_on_desc') : $t('ag_switch_off_desc') }}
        </div>
      </div>
      <div class="d-flex align-center">
        <v-switch
          :model-value="settings.enabled"
          :disabled="!canSwitch || busy"
          :loading="busy"
          color="success"
          hide-details
          inset
          :label="settings.enabled ? $t('ag_switch_on') : $t('ag_switch_off')"
          data-testid="agent-switch"
          @update:model-value="v => askSwitch(!!v)"
        />
      </div>
    </div>

    <div v-if="!canSwitch" class="text-caption text-medium-emphasis mt-2">{{ $t('ag_switch_admin_only') }}</div>

    <!-- Not copied yet: offer to copy the AI Engine prompts without switching -->
    <v-alert v-if="!settings.has_agents && !settings.enabled" type="info" variant="tonal" class="mt-4" data-testid="agent-migrate-hint">
      <div>{{ $t('ag_migrate_hint') }}</div>
      <v-btn
        v-if="canSwitch"
        class="mt-2"
        size="small"
        color="primary"
        variant="flat"
        prepend-icon="mdi-content-copy"
        :loading="busy"
        data-testid="agent-migrate"
        @click="migrate"
      >{{ $t('ag_migrate_button') }}</v-btn>
    </v-alert>

    <!-- Agents whose prompt differs from the AI Engine prompt it came from -->
    <v-alert
      v-if="settings.drift.length"
      :type="settings.enabled ? 'info' : 'warning'"
      variant="tonal"
      class="mt-4"
      data-testid="agent-drift"
    >
      <div class="font-weight-medium">{{ $t('ag_drift_title', { agents: driftNames }) }}</div>
      <div class="text-body-2 mt-1">{{ settings.enabled ? $t('ag_drift_body_on') : $t('ag_drift_body_off') }}</div>
      <v-btn
        v-if="canSwitch"
        class="mt-2"
        size="small"
        variant="outlined"
        prepend-icon="mdi-sync"
        :loading="busy"
        data-testid="agent-refresh"
        @click="confirmRefresh = true"
      >{{ $t('ag_drift_refresh') }}</v-btn>
    </v-alert>

    <v-dialog v-model="confirmSwitch" max-width="520">
      <v-card>
        <v-card-title class="text-h6">{{ pendingValue ? $t('ag_switch_confirm_on_title') : $t('ag_switch_confirm_off_title') }}</v-card-title>
        <v-card-text>
          <p v-if="pendingValue && !settings.has_agents">{{ $t('ag_switch_confirm_first') }}</p>
          <p v-else-if="pendingValue">{{ $t('ag_switch_confirm_on') }}</p>
          <p v-else>{{ $t('ag_switch_confirm_off') }}</p>
          <v-alert v-if="pendingValue && silentTeams.length" type="warning" variant="tonal" density="compact" class="mt-3">
            {{ $t('ag_switch_confirm_silent', { teams: silentTeams.join(', ') }) }}
          </v-alert>
          <v-alert v-if="pendingValue && settings.drift.length" type="warning" variant="tonal" density="compact" class="mt-3">
            {{ $t('ag_switch_confirm_drift', { agents: driftNames }) }}
          </v-alert>
        </v-card-text>
        <v-card-actions class="justify-end ga-2 pa-4">
          <v-btn variant="outlined" @click="confirmSwitch = false">{{ $t('cancel') }}</v-btn>
          <v-btn :color="pendingValue ? 'success' : 'error'" variant="flat" :loading="busy" data-testid="agent-switch-confirm" @click="applySwitch">
            {{ pendingValue ? $t('ag_switch_confirm_on_button') : $t('ag_switch_confirm_off_button') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="confirmRefresh" max-width="520">
      <v-card>
        <v-card-title class="text-h6">{{ $t('ag_drift_refresh_title') }}</v-card-title>
        <v-card-text>{{ $t('ag_drift_refresh_body', { agents: driftNames }) }}</v-card-text>
        <v-card-actions class="justify-end ga-2 pa-4">
          <v-btn variant="outlined" @click="confirmRefresh = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="warning" variant="flat" :loading="busy" data-testid="agent-refresh-confirm" @click="refresh">{{ $t('ag_drift_refresh') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-card>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import type { Agent, AgentSettings, Team } from './types'

const props = defineProps<{
  tenantId: string
  settings: AgentSettings
  canSwitch: boolean
  agents: Agent[]
  teams: Team[]
}>()

const emit = defineEmits<{
  'update:settings': [value: AgentSettings]
  // Agents or teams were created or changed on the server.
  changed: []
  notify: [text: string, color: string]
}>()

const { t } = useI18n()
const busy = ref(false)
const confirmSwitch = ref(false)
const confirmRefresh = ref(false)
const pendingValue = ref(false)

const driftNames = computed(() => props.settings.drift
  .map(key => props.agents.find(a => a.key === key)?.name || key)
  .join(', '))

// Teams with channels where someone would get no bot reply.
const silentTeams = computed(() => props.teams
  .filter(team => team.channels.length > 0 && team.silent.length > 0)
  .map(team => `${team.name} (${team.silent.map(a => t(`ag_audience_${a}`).toLowerCase()).join(', ')})`))

function askSwitch(value: boolean) {
  if (value === props.settings.enabled) return
  pendingValue.value = value
  confirmSwitch.value = true
}

async function applySwitch() {
  busy.value = true
  try {
    const { data } = await api.put(`/tenants/${props.tenantId}/settings/ai-agents`, { enabled: pendingValue.value })
    emit('update:settings', data)
    emit('changed')
    emit('notify', pendingValue.value ? t('ag_switched_on') : t('ag_switched_off'), 'success')
    confirmSwitch.value = false
  } catch {
    emit('notify', t('ag_switch_failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function migrate() {
  busy.value = true
  try {
    const { data } = await api.post(`/tenants/${props.tenantId}/settings/ai-agents/migrate`, {})
    emit('changed')
    emit('notify', data.created ? t('ag_migrated') : t('ag_migrate_nothing'), 'success')
    await reloadSettings()
  } catch {
    emit('notify', t('ag_switch_failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function refresh() {
  busy.value = true
  try {
    const { data } = await api.post(`/tenants/${props.tenantId}/settings/ai-agents/migrate`, { refresh: true })
    const refreshed: string[] = data.refreshed || []
    emit('changed')
    emit('notify', t('ag_refreshed', { n: refreshed.length }), 'success')
    confirmRefresh.value = false
    await reloadSettings()
  } catch {
    emit('notify', t('ag_switch_failed'), 'error')
  } finally {
    busy.value = false
  }
}

async function reloadSettings() {
  const { data } = await api.get(`/tenants/${props.tenantId}/settings/ai-agents`)
  emit('update:settings', data)
}

defineExpose({ askSwitch, applySwitch, migrate, refresh, confirmSwitch, confirmRefresh, pendingValue, silentTeams, driftNames })
</script>
