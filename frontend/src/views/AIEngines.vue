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

    <!-- ERP Config -->
    <v-card class="pa-6 mb-6">
      <div class="text-subtitle-1 font-weight-bold mb-4 d-flex align-center">
        <v-icon start size="small" color="primary" class="mr-2">mdi-database-sync</v-icon>
        {{ $t('erp_config_title') }}
      </div>

      <v-switch
        v-model="erp.active"
        :label="$t('erp_active')"
        color="primary"
        density="compact"
        class="mb-4"
      />

      <v-expand-transition>
        <div v-if="erp.active">
          <v-row>
            <v-col cols="12" md="6">
              <v-text-field
                v-model="erp.url"
                :label="$t('erp_url_label')"
                placeholder="https://api.cloudify.vn/api/v1"
                class="mb-3"
                clearable
              />
              <v-text-field
                v-model="erp.username"
                :label="$t('erp_username_label')"
                placeholder="cqa_agent_readonly"
                class="mb-3"
                clearable
              />
              <v-text-field
                v-model="erp.password"
                :label="$t('erp_password_label')"
                type="password"
                placeholder="••••••••"
                class="mb-3"
                clearable
              />
              <v-text-field
                v-model="erp.token"
                :label="$t('erp_token_label')"
                type="password"
                placeholder="••••••••"
                class="mb-3"
                clearable
              />
            </v-col>

            <v-col cols="12" md="6">
              <div class="text-subtitle-2 mb-2 font-weight-bold">{{ $t('erp_scopes_label') }}</div>
              
              <v-card variant="outlined" class="pa-4 rounded-lg bg-grey-lighten-5">
                <v-checkbox
                  v-model="erp.scopes"
                  value="read_products"
                  :label="$t('erp_scopes_products')"
                  density="compact"
                  hide-details
                  color="primary"
                  class="mb-1"
                />
                
                <v-checkbox
                  v-model="erp.scopes"
                  value="read_inventory"
                  :label="$t('erp_scopes_inventory')"
                  density="compact"
                  hide-details
                  color="primary"
                  class="mb-1"
                />
                
                <!-- Inventory category conditions filter -->
                <v-expand-transition>
                  <div v-if="erp.scopes.includes('read_inventory')" class="pl-8 pt-2 pb-2">
                    <v-text-field
                      v-model="erp.productGroups"
                      :label="$t('erp_inventory_product_groups')"
                      :hint="$t('erp_inventory_groups_hint')"
                      persistent-hint
                      density="compact"
                      variant="outlined"
                      class="mb-2"
                      placeholder="e.g. Nguyên Đầu, Nửa Đầu"
                    />
                  </div>
                </v-expand-transition>

                <v-checkbox
                  v-model="erp.scopes"
                  value="read_orders"
                  :label="$t('erp_scopes_orders')"
                  density="compact"
                  hide-details
                  color="primary"
                  class="mb-1"
                />
                
                <v-checkbox
                  v-model="erp.scopes"
                  value="read_customers"
                  :label="$t('erp_scopes_customers')"
                  density="compact"
                  hide-details
                  color="primary"
                />
              </v-card>
            </v-col>
          </v-row>

          <!-- Interactive Sơ đồ SVG Flow Chart Visual -->
          <div class="mt-6 mb-6">
            <div class="text-subtitle-2 mb-3 font-weight-bold">{{ $t('erp_flow_title') }}</div>
            <div class="erp-visual-graph pa-6 rounded-lg d-flex flex-column flex-sm-row justify-space-between align-center border">
              
              <!-- Langflow Agent Node -->
              <div class="graph-node pa-4 text-center rounded-xl elevation-2 bg-white border-2" style="width: 160px; border-color: #3f51b5">
                <v-icon color="primary" size="large" class="mb-2">mdi-robot-outline</v-icon>
                <div class="text-caption font-weight-bold">{{ $t('erp_langflow_node') }}</div>
                <div class="text-grey text-caption" style="font-size: 0.7rem !important">Zalo OA Chatflow</div>
              </div>

              <!-- Animated Data Path 1 -->
              <div class="graph-arrow flex-grow-1 mx-2 my-4 my-sm-0 position-relative text-center d-flex align-center justify-center">
                <div class="arrow-line" :class="{ 'animated-flow': erp.active }"></div>
                <v-icon class="arrow-tip" color="indigo">mdi-chevron-right</v-icon>
                <span class="text-caption text-indigo bg-white px-2 position-absolute" style="font-size: 0.65rem !important; top: -14px;">HTTP Query</span>
              </div>

              <!-- CQA Secure Gateway Node -->
              <div class="graph-node pa-4 text-center rounded-xl elevation-2 bg-white border-2" style="width: 200px; border-color: #4caf50">
                <v-icon color="success" size="large" class="mb-2">mdi-shield-check-outline</v-icon>
                <div class="text-caption font-weight-bold">{{ $t('erp_cqa_gateway') }}</div>
                <div class="mt-2 d-flex justify-center ga-1 flex-wrap">
                  <v-chip size="x-small" :color="erp.scopes.includes('read_products') ? 'success' : 'grey-lighten-2'" :variant="erp.scopes.includes('read_products') ? 'flat' : 'outlined'">Prod</v-chip>
                  <v-chip size="x-small" :color="erp.scopes.includes('read_inventory') ? 'success' : 'grey-lighten-2'" :variant="erp.scopes.includes('read_inventory') ? 'flat' : 'outlined'">Stock</v-chip>
                  <v-chip size="x-small" :color="erp.scopes.includes('read_orders') ? 'success' : 'grey-lighten-2'" :variant="erp.scopes.includes('read_orders') ? 'flat' : 'outlined'">Ord</v-chip>
                  <v-chip size="x-small" :color="erp.scopes.includes('read_customers') ? 'success' : 'grey-lighten-2'" :variant="erp.scopes.includes('read_customers') ? 'flat' : 'outlined'">Cust</v-chip>
                </div>
              </div>

              <!-- Animated Data Path 2 -->
              <div class="graph-arrow flex-grow-1 mx-2 my-4 my-sm-0 position-relative text-center d-flex align-center justify-center">
                <div class="arrow-line" :class="{ 'animated-flow-success': erp.active && erp.scopes.length > 0 }"></div>
                <v-icon class="arrow-tip" :color="erp.scopes.length > 0 ? 'success' : 'grey'">mdi-chevron-right</v-icon>
                <span class="text-caption bg-white px-2 position-absolute" :class="erp.scopes.length > 0 ? 'text-success' : 'text-grey'" style="font-size: 0.65rem !important; top: -14px;">
                  {{ erp.scopes.length > 0 ? $t('erp_verified') : $t('erp_blocked') }}
                </span>
              </div>

              <!-- Cloudify ERP Node -->
              <div class="graph-node pa-4 text-center rounded-xl elevation-2 bg-white border-2" :style="{ width: '160px', borderColor: erp.scopes.length > 0 ? '#ff9800' : '#9e9e9e' }">
                <v-icon :color="erp.scopes.length > 0 ? 'warning' : 'grey'" size="large" class="mb-2">mdi-server-network</v-icon>
                <div class="text-caption font-weight-bold" :class="{ 'text-grey-darken-1': erp.scopes.length === 0 }">{{ $t('erp_cloudify') }}</div>
                <div class="text-caption text-grey mt-1" style="font-size: 0.7rem !important" v-if="erp.scopes.includes('read_inventory') && erp.productGroups">
                  {{ erp.productGroups.split(',').filter(Boolean).length }} Groups Filter
                </div>
                <div class="text-caption text-grey mt-1" style="font-size: 0.7rem !important" v-else>
                  {{ erp.scopes.length > 0 ? $t('erp_active_route') : $t('erp_inactive_route') }}
                </div>
              </div>

            </div>
          </div>

          <!-- Gateway credentials for Langflow node -->
          <v-alert
            color="info"
            variant="tonal"
            icon="mdi-information-outline"
            class="mb-4"
          >
            <div class="text-subtitle-2 font-weight-bold mb-2">{{ $t('erp_agent_credentials') }}</div>
            
            <div class="mb-3">
              <div class="text-caption text-grey-darken-2 font-weight-bold mb-1">{{ $t('erp_gateway_url_label') }}</div>
              <v-text-field
                :model-value="gatewayUrl"
                readonly
                append-inner-icon="mdi-content-copy"
                @click:append-inner="copyToClipboard(gatewayUrl, 'Đã copy URL Endpoint')"
                density="compact"
                variant="outlined"
                hide-details
                bg-color="white"
              />
            </div>

            <div>
              <div class="text-caption text-grey-darken-2 font-weight-bold mb-1">{{ $t('erp_agent_token_label') }}</div>
              <v-text-field
                :model-value="erp.agentToken"
                readonly
                append-inner-icon="mdi-content-copy"
                @click:append-inner="copyToClipboard(erp.agentToken, 'Đã copy Agent Token')"
                density="compact"
                variant="outlined"
                hide-details
                bg-color="white"
              />
              <div class="text-caption text-grey-darken-2 mt-1">{{ $t('erp_agent_token_hint') }}</div>
            </div>
          </v-alert>

          <div class="d-flex ga-2 mt-4">
            <v-btn color="primary" :loading="savingERP" @click="saveERP">{{ $t('save') }}</v-btn>
            <v-btn variant="outlined" :loading="testingERP" @click="testERPConnection">
              {{ $t('erp_connection_test') }}
            </v-btn>
          </div>
        </div>
      </v-expand-transition>
    </v-card>

    <v-snackbar v-model="snackbar" :color="snackColor" timeout="3000">{{ snackText }}</v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, computed, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import api from '../api'

const route = useRoute()
const { t } = useI18n()
const tenantId = computed(() => route.params.tenantId as string)

const showKey = ref(false)
const snackbar = ref(false)
const snackText = ref('')
const snackColor = ref('success')

const saving = ref(false)
const testing = ref(false)

const savingERP = ref(false)
const testingERP = ref(false)

const langflow = reactive({
  baseUrl: '',
  flowId: '',
  publicFlowId: '',
  token: '',
})

const erp = reactive({
  active: false,
  url: '',
  token: '',
  username: '',
  password: '',
  productGroups: '',
  scopes: [] as string[],
  agentToken: '',
})

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

    erp.active = settings.erp_integration_active === 'true'
    erp.url = settings.erp_api_url || ''
    erp.username = settings.erp_api_username || ''
    erp.productGroups = settings.erp_inventory_product_groups || ''
    erp.agentToken = settings.ai_agent_erp_token || ''
    
    if (settings.erp_api_token) {
      erp.token = settings.erp_api_token
    }
    if (settings.erp_api_password) {
      erp.password = settings.erp_api_password
    }
    if (settings.erp_api_scopes) {
      erp.scopes = settings.erp_api_scopes.split(',').filter(Boolean)
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

async function saveERP() {
  savingERP.value = true
  try {
    await api.put(`/tenants/${tenantId.value}/settings/erp`, {
      active: erp.active ? 'true' : 'false',
      url: erp.url,
      token: erp.token,
      username: erp.username,
      password: erp.password,
      product_groups: erp.productGroups,
      scopes: erp.scopes.join(','),
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
  
  testingERP.value = true
  try {
    const { data } = await api.post(`/tenants/${tenantId.value}/settings/erp/test`, {
      url: erp.url,
      token: erp.token,
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

onMounted(loadSettings)
</script>

<style scoped>
.erp-visual-graph {
  background-color: #f8fafc;
  min-height: 120px;
  transition: all 0.3s ease;
}
.graph-node {
  transition: all 0.3s ease;
  z-index: 2;
}
.graph-node:hover {
  transform: translateY(-3px);
}
.graph-arrow {
  position: relative;
  height: 2px;
}
.arrow-line {
  height: 2px;
  background: #cbd5e1;
  width: 100%;
  position: absolute;
  top: 50%;
  left: 0;
  transform: translateY(-50%);
}
.animated-flow {
  background: linear-gradient(90deg, #3f51b5, #cbd5e1);
  background-size: 200% 100%;
  animation: dataflow 1.5s linear infinite;
}
.animated-flow-success {
  background: linear-gradient(90deg, #4caf50, #cbd5e1);
  background-size: 200% 100%;
  animation: dataflow 1.5s linear infinite;
}
.arrow-tip {
  position: absolute;
  right: -5px;
  top: 50%;
  transform: translateY(-50%);
  z-index: 1;
}
@keyframes dataflow {
  0% { background-position: 100% 0; }
  100% { background-position: -100% 0; }
}
</style>
