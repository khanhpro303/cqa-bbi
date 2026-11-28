<template>
  <v-dialog :model-value="modelValue" max-width="980" scrollable persistent @update:model-value="v => !v && requestClose()">
    <v-card data-testid="team-editor">
      <v-card-title class="editor-header d-flex align-center">
        <div class="header-icon mr-3">
          <v-icon color="primary" size="20">mdi-account-group-outline</v-icon>
        </div>
        <div class="d-flex align-center flex-wrap ga-2 min-w-0 flex-grow-1">
          <span class="editor-title text-truncate">{{ isNew ? $t('ag_new_team') : (draft.name || $t('ag_team')) }}</span>
          <v-chip v-if="team?.is_default" size="x-small" color="primary" variant="tonal">{{ $t('ag_team_default_badge') }}</v-chip>
        </div>
        <v-btn icon="mdi-close" variant="text" size="small" density="comfortable" class="ml-2 flex-shrink-0" :aria-label="$t('cancel')" @click="requestClose" />
      </v-card-title>
      <v-divider />

      <v-card-text class="editor-body">
        <v-alert v-if="readonly" type="info" variant="tonal" density="compact" class="mb-4">{{ $t('ag_readonly_hint') }}</v-alert>

        <div class="team-top-row mb-3">
          <div class="team-name-col">
            <v-text-field
              v-model="draft.name"
              :label="$t('ag_team_name')"
              :error-messages="errors.name"
              :readonly="readonly"
              counter="120"
              density="comfortable"
              variant="outlined"
              data-testid="team-name"
            />
          </div>
          <div class="team-status-col">
            <div class="status-pill" :class="{ 'status-pill--active': draft.is_active }">
              <v-switch
                v-model="draft.is_active"
                :label="draft.is_active ? $t('ag_active') : $t('ag_inactive')"
                :disabled="readonly || !!team?.is_default"
                density="compact"
                hide-details
                inset
                color="primary"
              />
            </div>
            <div v-if="team?.is_default" class="field-hint mt-1">{{ $t('ag_team_default_always_on') }}</div>
            <div v-if="errors.is_active" class="text-caption text-error mt-1">{{ errors.is_active }}</div>
          </div>
        </div>

        <v-textarea
          v-model="draft.persona_prompt"
          :label="$t('ag_team_persona')"
          :hint="$t('ag_team_persona_hint')"
          :placeholder="$t('ag_team_persona_placeholder')"
          persistent-hint
          :error-messages="errors.persona_prompt"
          :readonly="readonly"
          rows="4"
          auto-grow
          variant="outlined"
          class="prompt-field mb-2"
          data-testid="team-persona"
        />

        <!-- Members -->
        <div class="section-title mt-4">
          <v-icon size="small" class="mr-2">mdi-format-list-numbered</v-icon>
          {{ $t('ag_team_members') }}
        </div>
        <div class="text-caption text-medium-emphasis mb-2">{{ $t('ag_team_members_hint') }}</div>
        <v-alert v-if="errors.members || errors.default_agent_id" type="error" variant="tonal" density="compact" class="mb-2">
          {{ errors.members || errors.default_agent_id }}
        </v-alert>

        <v-card variant="outlined" class="mb-2">
          <div v-if="members.length === 0" class="text-center text-medium-emphasis pa-6">{{ $t('ag_team_no_members') }}</div>
          <div
            v-for="(m, idx) in members"
            :key="m.agent_id"
            class="member-row"
            :class="{ 'member-off': !m.enabled || !agentOf(m.agent_id)?.is_active }"
            :data-testid="`member-${agentOf(m.agent_id)?.key || m.agent_id}`"
          >
            <div class="member-pos tabular">{{ idx + 1 }}</div>
            <div class="member-main">
              <div class="d-flex align-center flex-wrap ga-1">
                <span class="font-weight-medium">{{ agentOf(m.agent_id)?.name || m.agent_id }}</span>
                <code class="text-caption text-medium-emphasis">{{ agentOf(m.agent_id)?.key }}</code>
                <v-chip v-if="agentOf(m.agent_id)" size="x-small" variant="tonal" :color="audienceColor(agentOf(m.agent_id)!.audience)">
                  {{ $t(`ag_audience_${agentOf(m.agent_id)!.audience}`) }}
                </v-chip>
                <v-chip v-if="agentOf(m.agent_id) && !agentOf(m.agent_id)!.is_active" size="x-small" color="grey" variant="tonal">{{ $t('ag_agent_off') }}</v-chip>
                <v-chip v-if="draft.default_agent_id === m.agent_id" size="x-small" color="primary" variant="flat" prepend-icon="mdi-star">{{ $t('ag_team_default_agent') }}</v-chip>
              </div>
            </div>
            <div class="member-actions">
              <v-switch
                v-model="m.enabled"
                :disabled="readonly"
                density="compact"
                hide-details
                color="primary"
                :aria-label="$t('ag_team_member_enabled')"
                class="mr-1"
              />
              <v-btn
                :icon="draft.default_agent_id === m.agent_id ? 'mdi-star' : 'mdi-star-outline'"
                size="small"
                variant="text"
                :color="draft.default_agent_id === m.agent_id ? 'primary' : undefined"
                :disabled="readonly"
                :aria-label="$t('ag_team_set_default')"
                :title="$t('ag_team_set_default')"
                @click="draft.default_agent_id = draft.default_agent_id === m.agent_id ? '' : m.agent_id"
              />
              <v-btn icon="mdi-arrow-up" size="small" variant="text" :disabled="readonly || idx === 0" :aria-label="$t('ag_move_up')" @click="move(idx, idx - 1)" />
              <v-btn icon="mdi-arrow-down" size="small" variant="text" :disabled="readonly || idx === members.length - 1" :aria-label="$t('ag_move_down')" @click="move(idx, idx + 1)" />
              <v-btn icon="mdi-close" size="small" variant="text" :disabled="readonly" :aria-label="$t('ag_team_remove_member')" @click="removeMember(idx)" />
            </div>
          </div>
        </v-card>

        <v-autocomplete
          v-if="!readonly"
          v-model="addAgentId"
          :items="addableAgents"
          item-title="name"
          item-value="id"
          :label="$t('ag_team_add_member')"
          :no-data-text="$t('ag_team_no_more_agents')"
          prepend-inner-icon="mdi-plus"
          density="comfortable"
          clearable
          data-testid="team-add-member"
          @update:model-value="addMember"
        >
          <template #item="{ props: itemProps, item }">
            <v-list-item v-bind="itemProps" :subtitle="`${item.key} · ${$t(`ag_audience_${item.audience}`)}`" />
          </template>
        </v-autocomplete>

        <!-- Who answers -->
        <div class="section-title mt-4">
          <v-icon size="small" class="mr-2">mdi-account-question-outline</v-icon>
          {{ $t('ag_team_who_answers') }}
        </div>
        <v-table density="compact" class="mb-2" data-testid="team-who-answers">
          <tbody>
            <tr v-for="aud in (['customer', 'staff'] as const)" :key="aud">
              <td class="text-medium-emphasis" style="width: 45%">{{ $t(`ag_sender_${aud}`) }}</td>
              <td>
                <span v-if="picks[aud]" class="font-weight-medium">{{ picks[aud]!.name }}</span>
                <v-chip v-else size="small" color="warning" variant="tonal" prepend-icon="mdi-volume-off">{{ $t('ag_silent') }}</v-chip>
              </td>
            </tr>
          </tbody>
        </v-table>
        <v-alert v-for="aud in silent" :key="aud" type="warning" variant="tonal" density="compact" class="mb-2" :data-testid="`team-silent-${aud}`">
          {{ $t(`ag_silent_warning_${aud}`) }}
        </v-alert>

        <div v-if="team && team.channels.length" class="mt-4">
          <div class="text-subtitle-2 mb-1">{{ $t('ag_team_channels') }}</div>
          <div class="d-flex flex-wrap ga-1">
            <v-chip v-for="ch in team.channels" :key="ch.id" size="small" variant="outlined" :prepend-icon="channelIcon(ch.channel_type)">{{ ch.name }}</v-chip>
          </div>
          <div class="text-caption text-medium-emphasis mt-1">{{ $t('ag_team_channels_hint') }}</div>
        </div>

        <!-- How the coordinator picks among the team's agents -->
        <v-expansion-panels class="mt-4" variant="accordion">
          <v-expansion-panel>
            <v-expansion-panel-title>
              <v-icon size="small" class="mr-2">mdi-routes</v-icon>
              {{ $t('ag_team_routing') }}
            </v-expansion-panel-title>
            <v-expansion-panel-text>
              <v-alert type="info" variant="tonal" density="compact" class="mb-3">{{ $t('ag_team_routing_note') }}</v-alert>
              <v-textarea
                v-model="draft.router_prompt"
                :label="$t('ag_team_router_prompt')"
                :hint="$t('ag_team_router_prompt_hint')"
                persistent-hint
                :readonly="readonly"
                rows="4"
                auto-grow
                variant="outlined"
                class="prompt-field mb-3"
              />
              <v-row dense>
                <v-col cols="12" md="6">
                  <v-text-field
                    v-model.number="draft.options.sticky_minutes"
                    type="number"
                    min="0"
                    max="1440"
                    :label="$t('ag_team_sticky')"
                    :hint="$t('ag_team_sticky_hint')"
                    persistent-hint
                    :error-messages="errors['options.sticky_minutes']"
                    :readonly="readonly"
                  />
                </v-col>
                <v-col cols="12" md="6">
                  <v-switch v-model="draft.options.disable_handoff" :label="$t('ag_team_disable_handoff')" color="primary" :disabled="readonly" hide-details />
                </v-col>
              </v-row>
            </v-expansion-panel-text>
          </v-expansion-panel>
        </v-expansion-panels>
      </v-card-text>

      <v-divider />
      <v-card-actions class="editor-footer ga-3 flex-wrap">
        <v-tooltip v-if="!isNew && !readonly" :disabled="!deleteBlockedReason" location="top">
          <template #activator="{ props: tip }">
            <span v-bind="tip">
              <v-btn color="error" variant="text" prepend-icon="mdi-delete-outline" :disabled="!!deleteBlockedReason || saving" data-testid="team-delete" @click="confirmDelete = true">{{ $t('delete') }}</v-btn>
            </span>
          </template>
          {{ deleteBlockedReason }}
        </v-tooltip>
        <v-spacer />
        <v-btn variant="outlined" class="footer-btn" @click="requestClose">{{ readonly ? $t('ag_close') : $t('cancel') }}</v-btn>
        <v-btn v-if="!readonly" color="primary" variant="flat" class="footer-btn" :loading="saving" data-testid="team-save" @click="save">{{ $t('save') }}</v-btn>
      </v-card-actions>
    </v-card>

    <v-dialog v-model="confirmDelete" max-width="460">
      <v-card>
        <v-card-title class="text-h6">{{ $t('ag_team_delete_title') }}</v-card-title>
        <v-card-text>{{ $t('ag_team_delete_body', { name: draft.name }) }}</v-card-text>
        <v-card-actions class="justify-end ga-2 pa-4">
          <v-btn variant="outlined" @click="confirmDelete = false">{{ $t('cancel') }}</v-btn>
          <v-btn color="error" variant="flat" :loading="deleting" data-testid="team-delete-confirm" @click="remove">{{ $t('delete') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="confirmDiscard" max-width="420">
      <v-card>
        <v-card-title class="text-h6">{{ $t('ag_discard_title') }}</v-card-title>
        <v-card-text>{{ $t('ag_discard_body') }}</v-card-text>
        <v-card-actions class="justify-end ga-2 pa-4">
          <v-btn variant="outlined" @click="confirmDiscard = false">{{ $t('ag_keep_editing') }}</v-btn>
          <v-btn color="error" variant="flat" @click="close">{{ $t('ag_discard') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import api from '../../api'
import type { Agent, MemberInput, Team, TeamInput } from './types'
import { errorCodeOf, fieldErrorOf } from './types'
import { moveItem, pickAgent, silentAudiences } from './agentRules'

const props = defineProps<{
  modelValue: boolean
  tenantId: string
  // null for a new team.
  team: Team | null
  agents: Agent[]
  readonly?: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  saved: [team: Team]
  deleted: [id: string]
  notify: [text: string, color: string]
}>()

const { t } = useI18n()

function emptyInput(): TeamInput {
  return { name: '', persona_prompt: '', router_prompt: '', default_agent_id: '', is_active: true, options: {}, members: [] }
}

const draft = ref<TeamInput>(emptyInput())
const members = ref<MemberInput[]>([])
const snapshot = ref('')
const errors = ref<Record<string, string>>({})
const saving = ref(false)
const deleting = ref(false)
const confirmDelete = ref(false)
const confirmDiscard = ref(false)
const addAgentId = ref<string | null>(null)

const isNew = computed(() => !props.team)
const agentsById = computed(() => new Map(props.agents.map(a => [a.id, a])))
function agentOf(id: string): Agent | undefined {
  return agentsById.value.get(id) || props.team?.members.find(a => a.id === id)
}

const addableAgents = computed(() => props.agents.filter(a => !members.value.some(m => m.agent_id === a.id)))

const memberStates = computed(() => members.value
  .map(m => ({ agent: agentOf(m.agent_id), enabled: m.enabled }))
  .filter((m): m is { agent: Agent, enabled: boolean } => !!m.agent))
const silent = computed(() => silentAudiences(memberStates.value))
const picks = computed(() => ({
  customer: pickAgent(memberStates.value, draft.value.default_agent_id, 'customer'),
  staff: pickAgent(memberStates.value, draft.value.default_agent_id, 'staff'),
}))

// Channels pointing at the team itself block deleting it; channels that
// only fall back to the default team do not (and the default never goes).
const deleteBlockedReason = computed(() => {
  if (!props.team) return ''
  if (props.team.is_default) return t('ag_team_delete_default')
  const own = props.team.channels.filter(ch => ch.ai_team_id === props.team!.id)
  return own.length ? t('ag_team_delete_has_channels', { channels: own.map(ch => ch.name).join(', ') }) : ''
})

function serialize(): string {
  return JSON.stringify({ ...draft.value, members: members.value })
}
const dirty = computed(() => snapshot.value !== '' && serialize() !== snapshot.value)

function audienceColor(a: string): string {
  return a === 'staff' ? 'indigo' : a === 'all' ? 'teal' : 'primary'
}
function channelIcon(type: string): string {
  return type === 'zalo_oa' ? 'mdi-alpha-z-box-outline' : 'mdi-account-circle-outline'
}

function load() {
  errors.value = {}
  addAgentId.value = null
  const team = props.team
  if (!team) {
    draft.value = emptyInput()
    members.value = []
  } else {
    draft.value = {
      name: team.name,
      persona_prompt: team.persona_prompt || '',
      router_prompt: team.router_prompt || '',
      default_agent_id: team.default_agent_id || '',
      is_active: team.is_active,
      options: { ...(team.options || {}) },
      members: [],
    }
    const memberships = [...(team.memberships || [])].sort((a, b) => a.position - b.position)
    members.value = memberships.length
      ? memberships.map(m => ({ agent_id: m.agent_id, position: m.position, enabled: m.enabled }))
      : team.members.map((a, i) => ({ agent_id: a.id, position: i, enabled: true }))
  }
  snapshot.value = serialize()
}

function move(from: number, to: number) {
  members.value = moveItem(members.value, from, to)
}
function removeMember(idx: number) {
  const [gone] = members.value.splice(idx, 1)
  if (gone && draft.value.default_agent_id === gone.agent_id) draft.value.default_agent_id = ''
}
function addMember(id: string | null) {
  if (!id) return
  if (!members.value.some(m => m.agent_id === id)) {
    members.value.push({ agent_id: id, position: members.value.length, enabled: true })
  }
  // Clear after the autocomplete has applied the selection.
  setTimeout(() => { addAgentId.value = null })
}

function payload(): TeamInput {
  const options = { ...draft.value.options }
  if ((options.sticky_minutes as unknown) === '' || options.sticky_minutes == null) delete options.sticky_minutes
  if (!options.disable_handoff) delete options.disable_handoff
  return {
    ...draft.value,
    name: draft.value.name.trim(),
    options,
    members: members.value.map((m, i) => ({ agent_id: m.agent_id, position: i, enabled: m.enabled })),
  }
}

async function save() {
  errors.value = {}
  if (!draft.value.name.trim()) {
    errors.value = { name: t('ag_err_name') }
    return
  }
  saving.value = true
  try {
    const body = payload()
    const { data } = isNew.value
      ? await api.post(`/tenants/${props.tenantId}/ai-teams`, body)
      : await api.put(`/tenants/${props.tenantId}/ai-teams/${props.team!.id}`, body)
    snapshot.value = serialize()
    emit('saved', data)
    emit('notify', t('ag_team_saved', { name: data.name }), 'success')
    close()
  } catch (err) {
    const fe = fieldErrorOf(err)
    if (fe) errors.value = { [fe.field]: fe.message }
    else emit('notify', t('ag_save_failed'), 'error')
  } finally {
    saving.value = false
  }
}

async function remove() {
  if (!props.team) return
  deleting.value = true
  try {
    await api.delete(`/tenants/${props.tenantId}/ai-teams/${props.team.id}`)
    confirmDelete.value = false
    emit('deleted', props.team.id)
    emit('notify', t('ag_team_deleted'), 'success')
    close()
  } catch (err) {
    confirmDelete.value = false
    emit('notify', errorCodeOf(err) === 'in_use' ? t('ag_team_delete_in_use') : t('ag_save_failed'), 'error')
  } finally {
    deleting.value = false
  }
}

function requestClose() {
  if (!props.readonly && dirty.value) {
    confirmDiscard.value = true
    return
  }
  close()
}
function close() {
  confirmDiscard.value = false
  emit('update:modelValue', false)
}

watch(() => [props.modelValue, props.team?.id] as const, ([open]) => {
  if (open) load()
}, { immediate: true })

defineExpose({ draft, members, errors, silent, picks, save, move, addMember, removeMember, dirty, deleteBlockedReason })
</script>

<style scoped>
.editor-header {
  padding: 16px 24px;
}
.header-icon {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border-radius: 10px;
  background: rgba(var(--v-theme-primary), 0.1);
  flex-shrink: 0;
}
.editor-title {
  font-size: 1.125rem;
  font-weight: 600;
  line-height: 1.35;
  letter-spacing: -0.01em;
}
.editor-body {
  padding: 20px 24px 24px;
}
.editor-body :deep(.v-field__input) {
  font-size: 0.875rem;
  line-height: 1.5;
}
.editor-body :deep(.v-label) {
  font-size: 0.875rem;
}
.editor-body :deep(.v-input__details) {
  padding-top: 5px;
}
.editor-body :deep(.v-messages__message) {
  font-size: 0.75rem;
  line-height: 1.45;
}
.field-hint {
  font-size: 0.75rem;
  line-height: 1.45;
  color: rgba(var(--v-theme-on-surface), 0.62);
}
.team-top-row {
  display: flex;
  align-items: flex-start;
  gap: 16px;
  flex-wrap: wrap;
}
.team-name-col {
  flex: 1 1 320px;
  min-width: 0;
}
.team-status-col {
  flex: 0 0 auto;
  min-width: 170px;
}
.status-pill {
  display: inline-flex;
  align-items: center;
  min-height: 48px;
  padding: 0 14px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 10px;
  background: rgba(var(--v-theme-on-surface), 0.025);
  transition: border-color 0.15s ease, background-color 0.15s ease;
}
.status-pill--active {
  border-color: rgba(var(--v-theme-primary), 0.28);
  background: rgba(var(--v-theme-primary), 0.04);
}
.status-pill :deep(.v-label) {
  font-size: 0.875rem;
  font-weight: 500;
  opacity: 1;
}
.section-title {
  display: flex;
  align-items: center;
  font-weight: 600;
  font-size: 0.9375rem;
  line-height: 1.4;
  margin-bottom: 4px;
}
.prompt-field :deep(textarea) {
  font-size: 0.875rem;
  line-height: 1.6;
}
.member-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 12px;
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}
.member-row:last-child {
  border-bottom: none;
}
.member-off .member-main {
  opacity: 0.55;
}
.member-pos {
  width: 20px;
  text-align: right;
  color: rgba(var(--v-theme-on-surface), 0.6);
  flex-shrink: 0;
}
.member-main {
  flex: 1;
  min-width: 0;
}
.member-actions {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-shrink: 0;
}
.editor-footer {
  padding: 14px 24px;
}
.footer-btn {
  min-width: 92px;
}
.tabular {
  font-variant-numeric: tabular-nums;
}
@media (max-width: 600px) {
  .editor-header {
    padding: 14px 16px;
  }
  .editor-body {
    padding: 16px;
  }
  .editor-footer {
    padding: 12px 16px;
  }
  .team-status-col {
    width: 100%;
  }
  .member-row {
    flex-wrap: wrap;
  }
  .member-actions {
    width: 100%;
    justify-content: flex-end;
  }
}
</style>
