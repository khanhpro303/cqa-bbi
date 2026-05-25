<template>
  <div>
    <h1 class="text-h5 font-weight-bold mb-6">{{ $t('ai_engines') }}</h1>
    <div class="text-grey-darken-1 mb-6">{{ $t('ai_engines_desc') }}</div>

    <!-- Langflow Config -->
    <v-card class="pa-6 mb-6">
      <div class="text-subtitle-1 font-weight-bold mb-4">
        <v-icon start size="small">mdi-brain</v-icon>
        {{ $t('langflow_config') }}
      </div>

      <v-text-field
        v-model="langflow.baseUrl"
        :label="$t('base_url')"
        placeholder="https://langflow.example.com"
        hint="URL của Langflow server (Bỏ dấu / ở cuối)"
        persistent-hint
        clearable
        class="mb-3"
      />

      <v-row>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="langflow.flowId"
            :label="$t('flow_id')"
            placeholder="e.g. 12345678-1234-1234-1234-123456789012"
            class="mb-3"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="langflow.publicFlowId"
            label="Flow ID (Public / General Users)"
            placeholder="e.g. 87654321-4321-4321-4321-210987654321"
            class="mb-3"
          />
        </v-col>
      </v-row>

      <v-text-field
        v-model="langflow.token"
        :label="$t('application_token')"
        :type="showKey ? 'text' : 'password'"
        :append-inner-icon="showKey ? 'mdi-eye-off' : 'mdi-eye'"
        @click:append-inner="showKey = !showKey"
        placeholder="••••••••"
        class="mb-3"
      />

      <div class="d-flex ga-2 mt-4">
        <v-btn color="primary" :loading="saving" @click="save">{{ $t('save') }}</v-btn>
        <v-btn variant="outlined" :loading="testing" @click="testConnection">
          {{ $t('test_connection') }}
        </v-btn>
      </div>
    </v-card>

    <!-- Astra DB Config -->
    <v-card class="pa-6 mb-6">
      <div class="text-subtitle-1 font-weight-bold mb-4 d-flex align-center">
        <v-icon start size="small" color="primary" class="mr-2">mdi-database-cog</v-icon>
        Cấu hình Astra DB (AI Memory & Product Cache)
      </div>

      <v-row>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="astradb.apiEndpoint"
            label="Astra DB API Endpoint"
            placeholder="https://<database-id>-<region>.apps.astra.datastax.com"
            class="mb-3"
            clearable
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="astradb.token"
            label="Astra DB Application Token"
            type="password"
            placeholder="••••••••"
            class="mb-3"
            clearable
            density="compact"
          />
        </v-col>
      </v-row>

      <v-row>
        <v-col cols="12" md="4">
          <v-text-field
            v-model="astradb.keyspace"
            label="Keyspace"
            placeholder="cqa_bbi"
            class="mb-3"
            clearable
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="4">
          <v-text-field
            v-model="astradb.collection"
            label="Chat History Collection"
            placeholder="zalo_chat_history"
            class="mb-3"
            clearable
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="4">
          <v-text-field
            v-model="astradb.productCollection"
            label="Product Cache Collection"
            placeholder="erp_product_bbi"
            class="mb-3"
            clearable
            density="compact"
          />
        </v-col>
      </v-row>

      <div class="d-flex ga-2 mt-4">
        <v-btn color="primary" :loading="saving" @click="save">{{ $t('save') }}</v-btn>
      </div>
    </v-card>

    <!-- ERP Config -->
    <v-card class="pa-6 mb-6">
      <div class="text-subtitle-1 font-weight-bold mb-4 d-flex align-center">
        <v-icon start size="small" color="primary" class="mr-2">mdi-database-sync</v-icon>
        {{ $t('erp_config_title') }}
      </div>

      <!-- Part 1: ERP Cloudify Connection (Shared) -->
      <div class="text-subtitle-2 mb-3 font-weight-bold" :class="isDark ? 'text-grey-lighten-1' : 'text-grey-darken-3'">{{ $t('erp_part1_title') }}</div>
      
      <v-row class="mb-4">
        <v-col cols="12" md="6">
          <v-text-field
            v-model="erp.url"
            :label="$t('erp_url_label')"
            placeholder="https://bbiapi.cloudify.vn"
            class="mb-3"
            clearable
            density="compact"
          />
          <v-text-field
            v-model="erp.username"
            :label="$t('erp_username_label')"
            placeholder="bien.la@cloudify.vn"
            class="mb-3"
            clearable
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="erp.dbName"
            :label="$t('erp_db_label')"
            :hint="$t('erp_db_hint')"
            persistent-hint
            placeholder="demobienla"
            class="mb-3"
            clearable
            density="compact"
          />
          <v-text-field
            v-model="erp.password"
            :label="$t('erp_password_label')"
            type="password"
            placeholder="••••••••"
            class="mb-3"
            clearable
            density="compact"
          />
        </v-col>
      </v-row>

      <div class="d-flex ga-2 mb-6">
        <v-btn variant="outlined" size="small" :loading="testingERP" @click="testERPConnection">
          {{ $t('erp_connection_test') }}
        </v-btn>
      </div>

      <v-divider class="mb-4"></v-divider>

      <!-- Advanced Section Toggle -->
      <div 
        class="d-flex align-center cursor-pointer py-2 px-3 rounded advanced-header mb-4"
        @click="showAdvanced = !showAdvanced"
        style="user-select: none;"
      >
        <div class="text-subtitle-2 font-weight-bold d-flex align-center">
          <v-icon start size="small" class="mr-2">mdi-cog-outline</v-icon>
          Nâng cao
        </div>
        <v-spacer></v-spacer>
        <v-icon>{{ showAdvanced ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
      </div>

      <v-expand-transition>
        <div v-show="showAdvanced" class="mb-6">
          <div class="text-subtitle-2 font-weight-bold mb-3 d-flex align-center" :class="isDark ? 'text-grey-lighten-1' : 'text-grey-darken-3'">
            <v-icon start size="small" color="primary" class="mr-2">mdi-information-outline</v-icon>
            {{ $t('erp_agent_credentials') }}
          </div>
          
          <v-alert color="info" variant="tonal" class="mb-4">
            <div class="text-caption" :class="isDark ? 'text-grey-lighten-1' : 'text-grey-darken-2'">
              {{ $t('erp_agent_token_hint') }}
            </div>
          </v-alert>

          <v-row>
            <v-col cols="12" md="4">
              <v-text-field
                :model-value="gatewayUrl"
                label="CQA Secure Gateway Endpoint"
                readonly
                append-inner-icon="mdi-content-copy"
                @click:append-inner="copyToClipboard(gatewayUrl, 'Đã copy URL Endpoint')"
                density="compact"
                variant="outlined"
                class="mb-3"
                :bg-color="isDark ? '#2a2a2a' : 'white'"
              />
            </v-col>
            <v-col cols="12" md="4">
              <v-text-field
                :model-value="erp.publicAgentToken"
                label="Agent Secure Token (Public Bot)"
                readonly
                append-inner-icon="mdi-content-copy"
                @click:append-inner="copyToClipboard(erp.publicAgentToken, 'Đã copy Public Agent Token')"
                density="compact"
                variant="outlined"
                class="mb-3"
                :bg-color="isDark ? '#2a2a2a' : 'white'"
              />
            </v-col>
            <v-col cols="12" md="4">
              <v-text-field
                :model-value="erp.privateAgentToken"
                label="Agent Secure Token (Private/Whitelist Bot)"
                readonly
                append-inner-icon="mdi-content-copy"
                @click:append-inner="copyToClipboard(erp.privateAgentToken, 'Đã copy Private Agent Token')"
                density="compact"
                variant="outlined"
                class="mb-3"
                :bg-color="isDark ? '#2a2a2a' : 'white'"
              />
            </v-col>
          </v-row>

          <v-divider class="my-6"></v-divider>

          <!-- Part 2: Global Method Permissions -->
          <div class="text-subtitle-2 font-weight-bold mb-3 d-flex align-center" :class="isDark ? 'text-grey-lighten-1' : 'text-grey-darken-3'">
            <v-icon start size="small" color="primary" class="mr-2">mdi-api</v-icon>
            Cấu hình HTTP Method cho Endpoint (Global dùng chung)
          </div>
          
          <v-card variant="outlined" class="rounded-lg mb-6 pa-1" :bg-color="isDark ? '#2a2a2a' : 'white'">
            <v-table density="compact">
              <thead>
                <tr>
                  <th style="width: 50%;">Tài nguyên Endpoint</th>
                  <th style="width: 25%;" class="text-center">Cho phép GET</th>
                  <th style="width: 25%;" class="text-center">Cho phép POST</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(methods, resource) in globalMethodPermissions" :key="resource">
                  <td class="text-caption font-weight-bold py-2">{{ resourceLabels[resource] || resource }}</td>
                  <td class="text-center">
                    <v-checkbox
                      v-model="methods.get"
                      color="primary"
                      density="compact"
                      hide-details
                      class="d-inline-flex"
                    />
                  </td>
                  <td class="text-center">
                    <v-checkbox
                      v-model="methods.post"
                      color="primary"
                      density="compact"
                      hide-details
                      class="d-inline-flex"
                    />
                  </td>
                </tr>
              </tbody>
            </v-table>
          </v-card>

          <v-divider class="my-6"></v-divider>

          <!-- Part 3: Private Bot Permissions -->
          <div class="text-subtitle-2 font-weight-bold mb-3 d-flex align-center" :class="isDark ? 'text-grey-lighten-1' : 'text-grey-darken-3'">
            <v-icon start size="small" color="indigo" class="mr-2">mdi-robot-outline</v-icon>
            Phân quyền Endpoint cho Private Chatbot (Nhân viên nội bộ)
          </div>

          <v-card variant="outlined" class="rounded-lg pa-1" :bg-color="isDark ? '#2a2a2a' : 'white'">
            <v-table density="compact">
              <thead>
                <tr>
                  <th style="width: 30%;">Tài nguyên</th>
                  <th style="width: 15%;" class="text-center">Cho phép truy cập</th>
                  <th style="width: 25%;">Phạm vi dữ liệu</th>
                  <th style="width: 30%;">Bộ lọc nhóm sản phẩm (VTHH)</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="ep in privateEndpoints" :key="ep.resource">
                  <td class="text-caption font-weight-bold py-2">{{ resourceLabels[ep.resource] || ep.resource }}</td>
                  <td class="text-center">
                    <v-switch
                      v-model="ep.is_enabled"
                      color="indigo"
                      density="compact"
                      hide-details
                      class="d-inline-flex"
                    />
                  </td>
                  <td>
                    <v-select
                      v-model="ep.scope_type"
                      :items="scopeOptions"
                      density="compact"
                      variant="plain"
                      hide-details
                      :disabled="!ep.is_enabled"
                      style="font-size: 0.75rem;"
                    />
                  </td>
                  <td>
                    <v-select
                      v-if="ep.resource === 'products' || ep.resource === 'inventory'"
                      v-model="ep.product_groups_arr"
                      :items="listTenNhomVthh"
                      multiple
                      chips
                      density="compact"
                      variant="plain"
                      hide-details
                      :disabled="!ep.is_enabled"
                      placeholder="Chọn nhóm..."
                      style="font-size: 0.75rem;"
                    />
                    <span v-else class="text-caption text-grey-darken-1">—</span>
                  </td>
                </tr>
              </tbody>
            </v-table>
          </v-card>
        </div>
      </v-expand-transition>

      <v-divider class="mb-6"></v-divider>

      <!-- Action Save Settings -->
      <div class="d-flex ga-2">
        <v-btn color="primary" :loading="savingERP" @click="saveERP">{{ $t('save') }}</v-btn>
      </div>
    </v-card>

    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3000">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { useTheme } from 'vuetify'
import api from '../api'

const route = useRoute()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)

const theme = useTheme()
const isDark = computed(() => theme.global.current.value.dark)

const showKey = ref(false)
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')

const saving = ref(false)
const testing = ref(false)

const savingERP = ref(false)
const testingERP = ref(false)
const showAdvanced = ref(false)

const langflow = reactive({
  baseUrl: '',
  flowId: '',
  publicFlowId: '',
  token: '',
})

const erp = reactive({
  url: '',
  dbName: '',
  username: '',
  password: '',
  publicAgentToken: '',
  privateAgentToken: '',
})

const astradb = reactive({
  apiEndpoint: '',
  token: '',
  keyspace: '',
  collection: '',
  productCollection: '',
})

const listTenNhomVthh = ref<string[]>([])

const resourceLabels: Record<string, string> = {
  products: '🛍 Sản phẩm',
  inventory: '📦 Tồn kho',
  orders: '📋 Đơn hàng',
  customers: '👥 Khách hàng',
  debt: '💰 Công nợ',
}

const scopeOptions = [
  { title: 'Tất cả', value: 'all' },
  { title: 'Của họ (OWN)', value: 'own' },
  { title: 'Phân công (ASSIGNED)', value: 'assigned' },
]

const globalMethodPermissions = ref<Record<string, { get: boolean; post: boolean }>>({
  products: { get: true, post: true },
  inventory: { get: true, post: true },
  orders: { get: true, post: true },
  customers: { get: true, post: true },
  debt: { get: true, post: true },
})

const privateEndpoints = ref<any[]>([
  { resource: 'products', is_enabled: true, scope_type: 'all', product_groups_arr: [] },
  { resource: 'inventory', is_enabled: true, scope_type: 'all', product_groups_arr: [] },
  { resource: 'orders', is_enabled: true, scope_type: 'all', product_groups_arr: [] },
  { resource: 'customers', is_enabled: true, scope_type: 'all', product_groups_arr: [] },
  { resource: 'debt', is_enabled: true, scope_type: 'all', product_groups_arr: [] },
])

const gatewayUrl = computed(() => {
  return `${window.location.origin}/api/v1/tenants/${tenantId.value}/erp/query`
})

function copyToClipboard(text: string, msg: string) {
  navigator.clipboard.writeText(text)
  showSnack(msg, 'success')
}

async function loadSettings() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/settings`)
    const settings = data.settings || {}

    langflow.baseUrl = settings.ai_engine_langflow_url || ''
    langflow.flowId = settings.ai_engine_langflow_flow_id || ''
    langflow.publicFlowId = settings.ai_engine_langflow_public_flow_id || ''
    
    if (settings.ai_engine_langflow_token) {
      langflow.token = settings.ai_engine_langflow_token
    }

    erp.url = settings.erp_api_url || ''
    erp.dbName = settings.erp_api_db || ''
    erp.username = settings.erp_api_username || ''

    if (settings.erp_api_password) {
      erp.password = settings.erp_api_password
    }

    erp.publicAgentToken = settings.ai_agent_erp_token_public || ''
    erp.privateAgentToken = settings.ai_agent_erp_token_private || ''

    astradb.apiEndpoint = settings.astradb_api_endpoint || ''
    astradb.keyspace = settings.astradb_keyspace || ''
    astradb.productCollection = settings.astradb_product_collection || ''
    astradb.collection = settings.astradb_collection || ''
    if (settings.astradb_token) {
      astradb.token = settings.astradb_token
    }

    if (settings.erp_global_method_permissions) {
      try {
        const parsed = JSON.parse(settings.erp_global_method_permissions)
        globalMethodPermissions.value = {
          ...globalMethodPermissions.value,
          ...parsed
        }
      } catch (e) {
        // Ignore
      }
    }

    if (data.private_endpoints && data.private_endpoints.length > 0) {
      privateEndpoints.value = privateEndpoints.value.map(ep => {
        const dbEp = data.private_endpoints.find((d: any) => d.resource === ep.resource)
        if (dbEp) {
          const groupsArr = dbEp.product_groups 
            ? dbEp.product_groups.split(',').map((g: string) => g.trim()).filter((g: string) => g)
            : []
          return {
            ...ep,
            is_enabled: dbEp.is_enabled,
            scope_type: dbEp.scope_type || 'all',
            product_groups_arr: groupsArr
          }
        }
        return ep
      })
    }
  } catch {
    // Ignore
  }
}

async function save() {
  saving.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings/ai-engines`, {
      langflow_base_url: langflow.baseUrl,
      langflow_flow_id: langflow.flowId,
      langflow_public_flow_id: langflow.publicFlowId,
      langflow_token: langflow.token,

      astradb_api_endpoint: astradb.apiEndpoint,
      astradb_token: astradb.token,
      astradb_keyspace: astradb.keyspace,
      astradb_product_collection: astradb.productCollection,
      astradb_collection: astradb.collection,
    })
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    saving.value = false
  }
}

async function testConnection() {
  if (!langflow.baseUrl || !langflow.flowId) {
    showSnack('Vui lòng nhập Base URL và Flow ID', 'warning')
    return
  }
  
  testing.value = true
  try {
    const { data } = await api.post(`/tenants/${tenantId.value}/settings/ai-engines/test`, {
      langflow_base_url: langflow.baseUrl,
      langflow_flow_id: langflow.flowId,
      langflow_token: langflow.token,
    })
    showSnack(data.message || 'Kết nối thành công', 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.message || err.response?.data?.error || t('error'), 'error')
  } finally {
    testing.value = false
  }
}

async function fetchListTenNhomVthh() {
  try {
    const { data } = await api.get(`/tenants/${tenantId.value}/crm/list-ten-nhom-vthh`)
    listTenNhomVthh.value = data || []
  } catch (err: any) {
    // Ignore
  }
}

async function saveERP() {
  savingERP.value = true
  try {
    const privateEndpointsPayload = privateEndpoints.value.map(ep => {
      return {
        resource: ep.resource,
        is_enabled: ep.is_enabled,
        scope_type: ep.scope_type || 'all',
        product_groups: ep.product_groups_arr ? ep.product_groups_arr.join(',') : '',
      }
    })

    await api.put(`/tenants/${tenantId.value}/settings/erp`, {
      url: erp.url,
      db: erp.dbName,
      username: erp.username,
      password: erp.password,
      // Public bot settings defaulted to false as they are managed per crm group
      public_active: 'false',
      public_scopes: '',
      public_product_groups: '',
      // Private bot settings defaulted to true for staff integration
      private_active: 'true',
      private_scopes: '',
      private_product_groups: '',

      global_method_permissions: globalMethodPermissions.value,
      private_endpoints: privateEndpointsPayload
    })
    showSnack(t('success'), 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.error || t('error'), 'error')
  } finally {
    savingERP.value = false
  }
}

async function testERPConnection() {
  if (!erp.url) {
    showSnack('Vui lòng nhập URL của Cloudify API', 'warning')
    return
  }
  if (!erp.username) {
    showSnack('Vui lòng nhập tài khoản Cloudify', 'warning')
    return
  }

  testingERP.value = true
  try {
    const { data } = await api.post(`/tenants/${tenantId.value}/settings/erp/test`, {
      url: erp.url,
      db: erp.dbName,
      username: erp.username,
      password: erp.password,
    })
    showSnack(data.message || 'Kết nối thành công', 'success')
  } catch (err: any) {
    showSnack(err.response?.data?.message || err.response?.data?.error || t('error'), 'error')
  } finally {
    testingERP.value = false
  }
}

function showSnack(text: string, color: string) {
  snackText.value = text
  snackColor.value = color
  snackbar.value = true
}

onMounted(async () => {
  await loadSettings()
  await fetchListTenNhomVthh()
})
</script>

<style scoped>
/* Scoped styles are kept minimal here */
.advanced-header {
  transition: background-color 0.2s ease;
}
.advanced-header:hover {
  background-color: rgba(128, 128, 128, 0.08);
}
</style>
