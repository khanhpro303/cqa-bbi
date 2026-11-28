<template>
  <div>
    <div class="d-flex align-center flex-wrap ga-2 mb-2">
      <h1 class="text-h5 font-weight-bold">{{ $t('nav_ai_agents') }}</h1>
      <v-chip v-if="loaded" size="small" :color="settings.enabled ? 'success' : 'grey'" variant="tonal" data-testid="agents-state-chip">
        {{ settings.enabled ? $t('ag_switch_on') : $t('ag_switch_off') }}
      </v-chip>
    </div>
    <div class="text-grey-darken-1 mb-4">{{ $t('ag_page_desc') }}</div>

    <v-alert v-if="loadError" type="error" variant="tonal" class="mb-4">
      {{ loadError }}
      <template #append><v-btn size="small" variant="text" @click="loadAll">{{ $t('retry') }}</v-btn></template>
    </v-alert>

    <AgentSwitchCard
      v-if="loaded"
      v-model:settings="settings"
      :tenant-id="tenantId"
      :can-switch="canSwitch"
      :agents="agents"
      :teams="teams"
      @changed="reloadData"
      @notify="showSnack"
    />

    <v-tabs v-model="tab" color="primary" class="mb-4" show-arrows>
      <v-tab value="agents" data-testid="tab-agents">
        <v-icon start size="small">mdi-robot-outline</v-icon>{{ $t('ag_tab_agents') }}
        <v-chip size="x-small" class="ml-2" variant="tonal">{{ agents.length }}</v-chip>
      </v-tab>
      <v-tab value="teams" data-testid="tab-teams">
        <v-icon start size="small">mdi-account-group-outline</v-icon>{{ $t('ag_tab_teams') }}
        <v-chip size="x-small" class="ml-2" variant="tonal">{{ teams.length }}</v-chip>
      </v-tab>
      <v-tab value="channels" data-testid="tab-channels">
        <v-icon start size="small">mdi-connection</v-icon>{{ $t('ag_tab_channels') }}
        <v-chip size="x-small" class="ml-2" variant="tonal">{{ channelRows.length }}</v-chip>
      </v-tab>
    </v-tabs>

    <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-2" />

    <!-- Agents -->
    <v-card v-if="tab === 'agents'" class="pa-4 pa-sm-5" data-testid="panel-agents">
      <div class="d-flex align-center flex-wrap ga-3 mb-4">
        <div class="text-body-2 text-medium-emphasis flex-grow-1" style="min-width: 240px">{{ $t('ag_agents_intro') }}</div>
        <v-btn v-if="canEdit" color="primary" variant="flat" prepend-icon="mdi-plus" data-testid="agent-create" @click="openAgent('')">{{ $t('ag_create_agent') }}</v-btn>
      </div>
      <div style="overflow-x: auto; -webkit-overflow-scrolling: touch;">
        <v-table density="comfortable" hover style="min-width: 760px;">
          <thead>
            <tr>
              <th>{{ $t('ag_col_agent') }}</th>
              <th>{{ $t('ag_field_audience') }}</th>
              <th class="text-right">{{ $t('ag_col_tools') }}</th>
              <th>{{ $t('ag_col_runtime') }}</th>
              <th>{{ $t('ag_tab_teams') }}</th>
              <th>{{ $t('status') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="a in agents" :key="a.id" class="cursor-pointer" :data-testid="`agent-row-${a.key}`" @click="openAgent(a.id)">
              <td class="py-3">
                <div class="text-body-2 font-weight-semibold">{{ a.name }}</div>
                <div class="mt-1"><code class="agent-key-code">{{ a.key }}</code></div>
                <div v-if="a.description" class="text-caption text-medium-emphasis desc-cell mt-1">{{ a.description }}</div>
              </td>
              <td class="text-no-wrap"><v-chip size="small" variant="tonal" :color="audienceColor(a.audience)">{{ $t(`ag_audience_${a.audience}`) }}</v-chip></td>
              <td class="text-right tabular">
                <span v-if="a.tools.length">{{ a.tools.length }}</span>
                <span v-else class="text-medium-emphasis">{{ $t('ag_no_tools') }}</span>
              </td>
              <td>
                <div class="text-body-2">{{ a.runtime === 'native' ? $t('ag_runtime_native') : 'Langflow' }}</div>
                <div class="text-caption text-medium-emphasis">{{ a.langflow_flow_id ? $t('ag_own_flow') : $t('ag_engine_flow') }}</div>
              </td>
              <td>
                <div class="d-flex flex-wrap ga-1">
                  <v-chip v-for="name in teamsOfAgent(a.id)" :key="name" size="x-small" variant="outlined">{{ name }}</v-chip>
                  <span v-if="teamsOfAgent(a.id).length === 0" class="text-caption text-warning">{{ $t('ag_not_in_team') }}</span>
                </div>
              </td>
              <td class="text-no-wrap">
                <v-chip size="small" :color="a.is_active ? 'success' : 'grey'" variant="tonal">{{ a.is_active ? $t('ag_active') : $t('ag_inactive') }}</v-chip>
              </td>
            </tr>
            <tr v-if="loaded && agents.length === 0">
              <td colspan="6" class="text-center text-medium-emphasis py-8">{{ $t('ag_agents_empty') }}</td>
            </tr>
          </tbody>
        </v-table>
      </div>
    </v-card>

    <!-- Teams -->
    <div v-if="tab === 'teams'" data-testid="panel-teams">
      <div class="d-flex align-center flex-wrap ga-2 mb-3">
        <div class="text-body-2 text-medium-emphasis flex-grow-1" style="min-width: 240px">{{ $t('ag_teams_intro') }}</div>
        <v-btn v-if="canEdit" color="primary" prepend-icon="mdi-plus" :disabled="agents.length === 0" data-testid="team-create" @click="openTeam(null)">{{ $t('ag_create_team') }}</v-btn>
      </div>
      <v-card v-if="loaded && teams.length === 0" class="pa-8 text-center text-medium-emphasis">{{ $t('ag_teams_empty') }}</v-card>
      <v-row>
        <v-col v-for="team in teams" :key="team.id" cols="12" md="6">
          <v-card class="pa-4 h-100" :data-testid="`team-card-${team.id}`">
            <div class="d-flex align-center flex-wrap ga-1 mb-1">
              <div class="text-subtitle-1 font-weight-bold mr-1">{{ team.name }}</div>
              <v-chip v-if="team.is_default" size="x-small" color="primary" variant="tonal">{{ $t('ag_team_default_badge') }}</v-chip>
              <v-chip v-if="!team.is_active" size="x-small" color="grey" variant="tonal">{{ $t('ag_inactive') }}</v-chip>
              <v-spacer />
              <v-btn size="small" variant="text" :prepend-icon="canEdit ? 'mdi-pencil' : 'mdi-eye-outline'" @click="openTeam(team)">{{ canEdit ? $t('edit') : $t('ag_view') }}</v-btn>
            </div>
            <div class="text-caption text-medium-emphasis persona-preview mb-3">
              {{ team.persona_prompt ? team.persona_prompt : $t('ag_team_no_persona') }}
            </div>

            <div class="text-caption text-medium-emphasis mb-1">{{ $t('ag_team_members') }}</div>
            <div class="d-flex flex-wrap ga-1 mb-3">
              <v-chip
                v-for="(m, i) in orderedMembers(team)"
                :key="m.agent.id"
                size="small"
                :variant="m.on ? 'tonal' : 'outlined'"
                :color="m.on ? audienceColor(m.agent.audience) : 'grey'"
                :prepend-icon="team.default_agent_id === m.agent.id ? 'mdi-star' : undefined"
              >{{ i + 1 }}. {{ m.agent.name }}</v-chip>
              <span v-if="team.members.length === 0" class="text-caption text-warning">{{ $t('ag_team_no_members') }}</span>
            </div>

            <v-table density="compact" class="mb-2 who-table flex-shrink-0">
              <tbody>
                <tr v-for="aud in (['customer', 'staff'] as const)" :key="aud">
                  <td class="text-medium-emphasis">{{ $t(`ag_sender_${aud}`) }}</td>
                  <td>
                    <span v-if="pickFor(team, aud)" class="font-weight-medium">{{ pickFor(team, aud)!.name }}</span>
                    <v-chip v-else size="x-small" color="warning" variant="tonal" prepend-icon="mdi-volume-off">{{ $t('ag_silent') }}</v-chip>
                  </td>
                </tr>
              </tbody>
            </v-table>

            <v-alert
              v-if="team.silent.length && team.channels.length"
              type="warning"
              variant="tonal"
              density="compact"
              class="mb-2 flex-shrink-0"
              :data-testid="`team-silent-alert-${team.id}`"
            >
              {{ $t('ag_team_silent_on_channels', { audiences: team.silent.map(a => $t(`ag_audience_${a}`).toLowerCase()).join(', '), n: team.channels.length }) }}
            </v-alert>

            <div class="text-caption text-medium-emphasis mt-1">
              <v-icon size="x-small" class="mr-1">mdi-connection</v-icon>
              <template v-if="team.channels.length">{{ team.channels.map(c => c.name).join(', ') }}</template>
              <template v-else>{{ $t('ag_team_no_channels') }}</template>
            </div>
            <div v-if="canEdit && !team.is_default && team.is_active" class="mt-2">
              <v-btn
                size="small"
                variant="text"
                color="primary"
                prepend-icon="mdi-star-outline"
                :loading="settingDefault === team.id"
                :data-testid="`team-make-default-${team.id}`"
                @click="makeDefaultTeam(team)"
              >{{ $t('ag_team_make_default') }}</v-btn>
            </div>
          </v-card>
        </v-col>
      </v-row>
    </div>

    <!-- Channels -->
    <v-card v-if="tab === 'channels'" class="pa-4" data-testid="panel-channels">
      <div class="text-body-2 text-medium-emphasis mb-3">{{ $t('ag_channels_intro') }}</div>
      <v-alert v-if="!settings.enabled && channelRows.length" type="info" variant="tonal" density="compact" class="mb-3">{{ $t('ag_channels_off_note') }}</v-alert>
      <div style="overflow-x: auto; -webkit-overflow-scrolling: touch;">
        <v-table density="comfortable" style="min-width: 760px;">
          <thead>
            <tr>
              <th>{{ $t('ag_col_channel') }}</th>
              <th style="width: 340px">{{ $t('ag_col_team') }}</th>
              <th>{{ $t('ag_sender_customer') }}</th>
              <th>{{ $t('ag_sender_staff') }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in channelRows" :key="row.channel.id" :data-testid="`channel-row-${row.channel.id}`">
              <td class="py-2">
                <div class="font-weight-medium">{{ row.channel.name }}</div>
                <div class="text-caption text-medium-emphasis">{{ $t(`ag_channel_type_${row.channel.channel_type}`) }}</div>
              </td>
              <td>
                <v-select
                  :model-value="row.selectValue"
                  :items="teamItems"
                  density="compact"
                  hide-details
                  variant="outlined"
                  :disabled="!canEdit"
                  :loading="savingChannel === row.channel.id"
                  :data-testid="`channel-team-${row.channel.id}`"
                  @update:model-value="v => setChannelTeam(row.channel.id, v as string)"
                />
                <div v-if="row.ownInactive" class="text-caption text-warning mt-1">{{ $t('ag_channel_team_inactive') }}</div>
              </td>
              <td>
                <span v-if="row.picks.customer">{{ row.picks.customer.name }}</span>
                <v-chip v-else size="x-small" color="warning" variant="tonal" prepend-icon="mdi-volume-off">{{ $t('ag_silent') }}</v-chip>
              </td>
              <td>
                <span v-if="row.picks.staff">{{ row.picks.staff.name }}</span>
                <v-chip v-else size="x-small" color="warning" variant="tonal" prepend-icon="mdi-volume-off">{{ $t('ag_silent') }}</v-chip>
              </td>
            </tr>
            <tr v-if="loaded && channelRows.length === 0">
              <td colspan="4" class="text-center text-medium-emphasis py-8">{{ teams.length ? $t('ag_channels_empty') : $t('ag_channels_no_teams') }}</td>
            </tr>
          </tbody>
        </v-table>
      </div>
    </v-card>

    <AgentEditorDialog
      v-model="agentDialog"
      :tenant-id="tenantId"
      :agent-id="editingAgentId"
      :catalog="catalog"
      :runtimes="runtimes"
      :readonly="!canEdit"
      :team-names="editingAgentId ? teamsOfAgent(editingAgentId) : []"
      :user-names="userNames"
      :knowledge-tags="knowledgeTags"
      @saved="reloadData"
      @deleted="reloadData"
      @notify="showSnack"
    />

    <TeamEditorDialog
      v-model="teamDialog"
      :tenant-id="tenantId"
      :team="editingTeam"
      :agents="agents"
      :readonly="!canEdit"
      @saved="reloadData"
      @deleted="reloadData"
      @notify="showSnack"
    />

    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3500">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'
import { useAuthStore } from '../stores/auth'
import AgentSwitchCard from '../components/ai-agents/AgentSwitchCard.vue'
import AgentEditorDialog from '../components/ai-agents/AgentEditorDialog.vue'
import TeamEditorDialog from '../components/ai-agents/TeamEditorDialog.vue'
import type { Agent, AgentSettings, KnowledgeTag, Team, TeamChannel, ToolInfo } from '../components/ai-agents/types'
import { pickAgent } from '../components/ai-agents/agentRules'

const route = useRoute()
const router = useRouter()
const { t } = useI18n()
const authStore = useAuthStore()
const tenantId = computed(() => route.params.tenantId as string)

const canEdit = computed(() => authStore.canEdit('settings'))
const canSwitch = computed(() => ['owner', 'admin'].includes(authStore.tenantPerms?.role || ''))

const tab = ref<'agents' | 'teams' | 'channels'>('agents')
const loading = ref(false)
const loaded = ref(false)
const loadError = ref('')

const settings = ref<AgentSettings>({ enabled: false, has_agents: false, drift: [] })
const catalog = ref<ToolInfo[]>([])
const runtimes = ref<string[]>(['langflow'])
const agents = ref<Agent[]>([])
const teams = ref<Team[]>([])
const userNames = ref<Record<string, string>>({})
// null until the server lists document tags (older servers have no list).
const knowledgeTags = ref<KnowledgeTag[] | null>(null)

const agentDialog = ref(false)
const editingAgentId = ref('')
const teamDialog = ref(false)
const editingTeam = ref<Team | null>(null)
const savingChannel = ref('')

const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')

function showSnack(text: string, color = 'success') {
  snackText.value = text
  snackColor.value = color
  snackbar.value = true
}

function audienceColor(a: string): string {
  return a === 'staff' ? 'indigo' : a === 'all' ? 'teal' : 'primary'
}

const defaultTeam = computed(() => teams.value.find(team => team.is_default && team.is_active) || null)

function teamsOfAgent(agentId: string): string[] {
  return teams.value.filter(team => (team.memberships || []).some(m => m.agent_id === agentId) || team.members.some(a => a.id === agentId)).map(team => team.name)
}

// Members in team order with whether they can answer (membership on and
// agent active). Fresh agent data comes from the agent list.
function memberStates(team: Team) {
  const enabled = new Map((team.memberships || []).map(m => [m.agent_id, m.enabled]))
  return team.members.map(a => {
    const agent = agents.value.find(x => x.id === a.id) || a
    return { agent, enabled: enabled.get(a.id) ?? true }
  })
}
function orderedMembers(team: Team) {
  return memberStates(team).map(m => ({ agent: m.agent, on: m.enabled && m.agent.is_active }))
}
function pickFor(team: Team | null, audience: 'customer' | 'staff') {
  if (!team) return null
  return pickAgent(memberStates(team), team.default_agent_id, audience)
}

interface ChannelRow {
  channel: TeamChannel
  selectValue: string
  ownInactive: boolean
  picks: { customer: Agent | null, staff: Agent | null }
}

// Every chat channel appears under exactly one team: its own, or the
// default team when it follows the default.
const channelRows = computed<ChannelRow[]>(() => {
  const rows: ChannelRow[] = []
  for (const team of teams.value) {
    for (const ch of team.channels) {
      const own = ch.ai_team_id ? teams.value.find(x => x.id === ch.ai_team_id) || null : null
      const effective = own && own.is_active ? own : defaultTeam.value
      rows.push({
        channel: ch,
        selectValue: own && !own.is_default ? own.id : '',
        ownInactive: !!own && !own.is_active,
        picks: { customer: pickFor(effective, 'customer'), staff: pickFor(effective, 'staff') },
      })
    }
  }
  return rows.sort((a, b) => a.channel.name.localeCompare(b.channel.name, 'vi'))
})

const teamItems = computed(() => [
  { title: t('ag_channel_follow_default', { name: defaultTeam.value?.name || '—' }), value: '' },
  ...teams.value.filter(team => !team.is_default).map(team => ({
    title: team.is_active ? team.name : `${team.name} (${t('ag_inactive').toLowerCase()})`,
    value: team.id,
  })),
])

function openAgent(id: string) {
  editingAgentId.value = id
  agentDialog.value = true
}
function openTeam(team: Team | null) {
  editingTeam.value = team
  teamDialog.value = true
}

const settingDefault = ref('')
async function makeDefaultTeam(team: Team) {
  settingDefault.value = team.id
  try {
    await api.post(`/tenants/${tenantId.value}/ai-teams/${team.id}/default`)
    await loadTeams()
    showSnack(t('ag_team_default_set', { name: team.name }), 'success')
  } catch {
    showSnack(t('ag_save_failed'), 'error')
  } finally {
    settingDefault.value = ''
  }
}

async function setChannelTeam(channelId: string, teamId: string) {
  savingChannel.value = channelId
  try {
    await api.put(`/tenants/${tenantId.value}/channels/${channelId}/ai-team`, { team_id: teamId || '' })
    await loadTeams()
    showSnack(t('ag_channel_saved'), 'success')
  } catch {
    showSnack(t('ag_save_failed'), 'error')
  } finally {
    savingChannel.value = ''
  }
}

async function loadSettings() {
  const { data } = await api.get(`/tenants/${tenantId.value}/settings/ai-agents`)
  settings.value = { enabled: !!data.enabled, has_agents: !!data.has_agents, drift: data.drift || [] }
}
async function loadCatalog() {
  const { data } = await api.get(`/tenants/${tenantId.value}/ai-agent-tools`)
  catalog.value = data.tools || []
  runtimes.value = data.runtimes || ['langflow']
}
async function loadAgents() {
  const { data } = await api.get(`/tenants/${tenantId.value}/ai-agents`)
  agents.value = (data.agents || []).map((a: Agent) => ({ ...a, tools: a.tools || [], knowledge_tags: a.knowledge_tags || [] }))
}
async function loadTeams() {
  const { data } = await api.get(`/tenants/${tenantId.value}/ai-teams`)
  teams.value = (data.teams || []).map((team: Team) => ({
    ...team, members: team.members || [], memberships: team.memberships || [], channels: team.channels || [], silent: team.silent || [], options: team.options || {},
  }))
}
async function loadUsers() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/users`)
    userNames.value = Object.fromEntries((data || []).map((u: { user_id: string, name?: string, email?: string }) => [u.user_id, u.name || u.email || u.user_id]))
  } catch {
    // Names are only used to label revisions.
  }
}

async function loadKnowledgeTags() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/knowledge/tags`)
    knowledgeTags.value = Array.isArray(data?.tags) ? data.tags : null
  } catch {
    knowledgeTags.value = null
  }
}

async function reloadData() {
  try {
    await Promise.all([loadSettings(), loadAgents(), loadTeams()])
  } catch {
    showSnack(t('ag_load_failed'), 'error')
  }
}

async function loadAll() {
  loading.value = true
  loadError.value = ''
  try {
    await Promise.all([loadSettings(), loadCatalog(), loadAgents(), loadTeams()])
    loaded.value = true
  } catch {
    loadError.value = t('ag_load_failed')
  } finally {
    loading.value = false
  }
  loadUsers()
  loadKnowledgeTags()
}

// Keep the selected tab in the URL so a link opens the same tab.
watch(tab, v => {
  if (route.query?.tab !== v) router.replace({ query: { ...route.query, tab: v } })
})

onMounted(() => {
  const q = route.query?.tab
  if (q === 'teams' || q === 'channels' || q === 'agents') tab.value = q
  loadAll()
})

defineExpose({ settings, agents, teams, channelRows, teamItems, setChannelTeam, openAgent, openTeam, reloadData, tab })
</script>

<style scoped>
.cursor-pointer {
  cursor: pointer;
}
.tabular {
  font-variant-numeric: tabular-nums;
}
.agent-key-code {
  font-size: 0.75rem;
  padding: 1px 6px;
  border-radius: 4px;
  background: rgba(var(--v-theme-on-surface), 0.06);
  color: rgba(var(--v-theme-on-surface), 0.72);
}
.desc-cell {
  max-width: 420px;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}
.persona-preview {
  white-space: pre-wrap;
  display: -webkit-box;
  -webkit-line-clamp: 3;
  line-clamp: 3;
  -webkit-box-orient: vertical;
  overflow: hidden;
  min-height: 1.5em;
}
.who-table :deep(td) {
  height: 32px !important;
}
</style>
