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

      <!-- Part 1: ERP Cloudify Connection (Shared) -->
      <div class="text-subtitle-2 mb-3 font-weight-bold text-grey-darken-3">{{ $t('erp_part1_title') }}</div>
      
      <v-row class="mb-4">
        <v-col cols="12" md="6">
          <v-text-field
            v-model="erp.url"
            :label="$t('erp_url_label')"
            placeholder="https://api.cloudify.vn/api/v1"
            class="mb-3"
            clearable
            density="compact"
          />
          <v-text-field
            v-model="erp.username"
            :label="$t('erp_username_label')"
            placeholder="cqa_agent_readonly"
            class="mb-3"
            clearable
            density="compact"
          />
        </v-col>
        <v-col cols="12" md="6">
          <v-text-field
            v-model="erp.password"
            :label="$t('erp_password_label')"
            type="password"
            placeholder="••••••••"
            class="mb-3"
            clearable
            density="compact"
          />
          <v-text-field
            v-model="erp.token"
            :label="$t('erp_token_label')"
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

      <v-divider class="mb-6"></v-divider>

      <!-- Part 2: Dynamic Bot Configurations (Public vs Whitelist/Private) -->
      <div class="text-subtitle-2 mb-3 font-weight-bold text-grey-darken-3">{{ $t('erp_part2_title') }}</div>
      
      <v-tabs v-model="activeBotTab" color="primary" class="mb-4">
        <v-tab value="public" class="text-none">
          <v-icon start>mdi-account-multiple-outline</v-icon>
          {{ $t('erp_tab_public') }}
        </v-tab>
        <v-tab value="private" class="text-none">
          <v-icon start>mdi-shield-account-outline</v-icon>
          {{ $t('erp_tab_private') }}
        </v-tab>
      </v-tabs>

      <v-window v-model="activeBotTab" class="py-4">
        
        <!-- Public Bot Tab Panel -->
        <v-window-item value="public">
          <div class="px-3">
            <v-switch
              v-model="erp.publicActive"
              :label="$t('erp_active_public')"
              color="primary"
              density="compact"
              class="mb-4"
            />
          </div>
          
          <v-expand-transition>
            <div v-if="erp.publicActive">
              <v-row class="px-3">
                <v-col cols="12" md="6">
                  <div class="text-subtitle-2 mb-2 font-weight-bold">{{ $t('erp_scopes_label') }}</div>
                  <v-card variant="outlined" class="pa-4 rounded-lg bg-grey-lighten-5">
                    <v-checkbox
                      v-model="erp.publicScopes"
                      value="read_products"
                      :label="$t('erp_scopes_products')"
                      density="compact"
                      hide-details
                      color="primary"
                      class="mb-1"
                    />
                    <v-checkbox
                      v-model="erp.publicScopes"
                      value="read_inventory"
                      :label="$t('erp_scopes_inventory')"
                      density="compact"
                      hide-details
                      color="primary"
                      class="mb-1"
                    />
                    
                    <v-expand-transition>
                      <div v-if="erp.publicScopes.includes('read_inventory')" class="pl-8 pt-2 pb-2">
                        <v-text-field
                          v-model="erp.publicProductGroups"
                          :label="$t('erp_inventory_product_groups')"
                          :hint="$t('erp_inventory_groups_hint')"
                          persistent-hint
                          density="compact"
                          variant="outlined"
                          class="mb-2"
                          placeholder="e.g. Mỹ Phẩm"
                        />
                      </div>
                    </v-expand-transition>

                    <v-checkbox
                      v-model="erp.publicScopes"
                      value="read_orders"
                      :label="$t('erp_scopes_orders') + ' (Không khuyến nghị cho Public)'"
                      density="compact"
                      hide-details
                      color="primary"
                      class="mb-1"
                    />
                    <v-checkbox
                      v-model="erp.publicScopes"
                      value="read_customers"
                      :label="$t('erp_scopes_customers') + ' (Không khuyến nghị cho Public)'"
                      density="compact"
                      hide-details
                      color="primary"
                    />
                  </v-card>
                </v-col>
                
                <v-col cols="12" md="6">
                  <v-alert color="info" variant="tonal" icon="mdi-information-outline" class="h-100">
                    <div class="text-subtitle-2 font-weight-bold mb-2">{{ $t('erp_info_public') }}</div>
                    <v-text-field
                      :model-value="gatewayUrl"
                      label="Endpoint URL"
                      readonly
                      append-inner-icon="mdi-content-copy"
                      @click:append-inner="copyToClipboard(gatewayUrl, 'Đã copy URL Endpoint')"
                      density="compact"
                      variant="outlined"
                      class="mb-3"
                      bg-color="white"
                    />
                    <v-text-field
                      :model-value="erp.publicAgentToken"
                      label="Agent Secure Token (Public)"
                      readonly
                      append-inner-icon="mdi-content-copy"
                      @click:append-inner="copyToClipboard(erp.publicAgentToken, 'Đã copy Public Agent Token')"
                      density="compact"
                      variant="outlined"
                      bg-color="white"
                    />
                    <div class="text-caption text-grey-darken-2 mt-2">{{ $t('erp_agent_token_hint') }}</div>
                  </v-alert>
                </v-col>
              </v-row>

              <!-- Sơ đồ tương tác dữ liệu Live của AI Agent (Public) -->
              <div class="mt-6 mb-2 px-3">
                <div class="text-subtitle-2 mb-3 font-weight-bold">{{ $t('erp_flow_title') }}</div>
                <div class="erp-visual-graph pa-6 rounded-lg d-flex flex-column flex-sm-row justify-space-between align-center border">
                  <!-- Node Left: Public Agent -->
                  <div class="graph-node pa-3 text-center rounded-xl elevation-2 bg-white border-2" :style="{ width: '160px', borderColor: '#3f51b5' }">
                    <v-icon color="primary" size="small" class="mb-1">mdi-account-multiple-outline</v-icon>
                    <div class="text-caption font-weight-bold">Public Bot</div>
                    <div class="text-grey text-caption" style="font-size: 0.65rem !important">Khách hàng chat</div>
                  </div>

                  <!-- Path Left-to-Center -->
                  <div class="graph-arrow flex-grow-1 mx-2 my-2 my-sm-0 position-relative text-center d-flex align-center justify-center">
                    <div class="arrow-line animated-flow"></div>
                    <v-icon class="arrow-tip" color="indigo">mdi-chevron-right</v-icon>
                    <span class="text-caption bg-white px-2 position-absolute" style="font-size: 0.6rem !important; top: -14px;">X-Agent-Token (Public)</span>
                  </div>

                  <!-- Node Center: CQA Gateway Checks -->
                  <div class="graph-node pa-3 text-center rounded-xl elevation-2 bg-white border-2" :style="{ width: '200px', borderColor: '#4caf50' }">
                    <v-icon color="success" size="small" class="mb-1">mdi-shield-check-outline</v-icon>
                    <div class="text-caption font-weight-bold">Gateway (Public Scopes)</div>
                    <div class="mt-1 d-flex justify-center ga-1 flex-wrap">
                      <v-chip size="x-small" :color="erp.publicScopes.includes('read_products') ? 'success' : 'grey-lighten-2'">Prod</v-chip>
                      <v-chip size="x-small" :color="erp.publicScopes.includes('read_inventory') ? 'success' : 'grey-lighten-2'">Stock</v-chip>
                      <v-chip size="x-small" :color="erp.publicScopes.includes('read_orders') ? 'success' : 'grey-lighten-2'">Ord</v-chip>
                    </div>
                  </div>

                  <!-- Path Center-to-Right -->
                  <div class="graph-arrow flex-grow-1 mx-2 my-2 my-sm-0 position-relative text-center d-flex align-center justify-center">
                    <div class="arrow-line" :class="{ 'animated-flow-success': erp.publicScopes.length > 0 }"></div>
                    <v-icon class="arrow-tip" :color="erp.publicScopes.length > 0 ? 'success' : 'grey'">mdi-chevron-right</v-icon>
                    <span class="text-caption bg-white px-2 position-absolute" :class="erp.publicScopes.length > 0 ? 'text-success' : 'text-grey'" style="font-size: 0.6rem !important; top: -14px;">
                      {{ erp.publicScopes.length > 0 ? 'Cho phép' : 'Chặn' }}
                    </span>
                  </div>

                  <!-- Node Right: Cloudify ERP -->
                  <div class="graph-node pa-3 text-center rounded-xl elevation-2 bg-white border-2" :style="{ width: '150px', borderColor: erp.publicScopes.length > 0 ? '#ff9800' : '#9e9e9e' }">
                    <v-icon :color="erp.publicScopes.length > 0 ? 'warning' : 'grey'" size="small" class="mb-1">mdi-server-network</v-icon>
                    <div class="text-caption font-weight-bold" :class="{ 'text-grey': erp.publicScopes.length === 0 }">Cloudify ERP</div>
                    <div class="text-grey text-caption" style="font-size: 0.65rem !important" v-if="erp.publicScopes.includes('read_inventory') && erp.publicProductGroups">
                      Filter: {{ erp.publicProductGroups }}
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </v-expand-transition>
        </v-window-item>

        <!-- Private Bot Tab Panel -->
        <v-window-item value="private">
          <div class="px-3">
            <v-switch
              v-model="erp.privateActive"
              :label="$t('erp_active_private')"
              color="primary"
              density="compact"
              class="mb-4"
            />
          </div>
          
          <v-expand-transition>
            <div v-if="erp.privateActive">
              <v-row class="px-3">
                <v-col cols="12" md="6">
                  <div class="text-subtitle-2 mb-2 font-weight-bold">{{ $t('erp_scopes_label') }}</div>
                  <v-card variant="outlined" class="pa-4 rounded-lg bg-grey-lighten-5">
                    <v-checkbox
                      v-model="erp.privateScopes"
                      value="read_products"
                      :label="$t('erp_scopes_products')"
                      density="compact"
                      hide-details
                      color="primary"
                      class="mb-1"
                    />
                    <v-checkbox
                      v-model="erp.privateScopes"
                      value="read_inventory"
                      :label="$t('erp_scopes_inventory')"
                      density="compact"
                      hide-details
                      color="primary"
                      class="mb-1"
                    />
                    
                    <v-expand-transition>
                      <div v-if="erp.privateScopes.includes('read_inventory')" class="pl-8 pt-2 pb-2">
                        <v-text-field
                          v-model="erp.privateProductGroups"
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
                      v-model="erp.privateScopes"
                      value="read_orders"
                      :label="$t('erp_scopes_orders')"
                      density="compact"
                      hide-details
                      color="primary"
                      class="mb-1"
                    />
                    <v-checkbox
                      v-model="erp.privateScopes"
                      value="read_customers"
                      :label="$t('erp_scopes_customers')"
                      density="compact"
                      hide-details
                      color="primary"
                    />
                  </v-card>
                </v-col>
                
                <v-col cols="12" md="6">
                  <v-alert color="info" variant="tonal" icon="mdi-information-outline" class="h-100">
                    <div class="text-subtitle-2 font-weight-bold mb-2">{{ $t('erp_info_private') }}</div>
                    <v-text-field
                      :model-value="gatewayUrl"
                      label="Endpoint URL"
                      readonly
                      append-inner-icon="mdi-content-copy"
                      @click:append-inner="copyToClipboard(gatewayUrl, 'Đã copy URL Endpoint')"
                      density="compact"
                      variant="outlined"
                      class="mb-3"
                      bg-color="white"
                    />
                    <v-text-field
                      :model-value="erp.privateAgentToken"
                      label="Agent Secure Token (Private)"
                      readonly
                      append-inner-icon="mdi-content-copy"
                      @click:append-inner="copyToClipboard(erp.privateAgentToken, 'Đã copy Private Agent Token')"
                      density="compact"
                      variant="outlined"
                      bg-color="white"
                    />
                    <div class="text-caption text-grey-darken-2 mt-2">{{ $t('erp_agent_token_hint') }}</div>
                  </v-alert>
                </v-col>
              </v-row>

              <!-- Sơ đồ tương tác dữ liệu Live của AI Agent (Private) -->
              <div class="mt-6 mb-2 px-3">
                <div class="text-subtitle-2 mb-3 font-weight-bold">{{ $t('erp_flow_title') }}</div>
                <div class="erp-visual-graph pa-6 rounded-lg d-flex flex-column flex-sm-row justify-space-between align-center border">
                  <!-- Node Left: Private Agent -->
                  <div class="graph-node pa-3 text-center rounded-xl elevation-2 bg-white border-2" :style="{ width: '160px', borderColor: '#3f51b5' }">
                    <v-icon color="primary" size="small" class="mb-1">mdi-shield-account-outline</v-icon>
                    <div class="text-caption font-weight-bold">Private Bot</div>
                    <div class="text-grey text-caption" style="font-size: 0.65rem !important">Nhân viên chat</div>
                  </div>

                  <!-- Path Left-to-Center -->
                  <div class="graph-arrow flex-grow-1 mx-2 my-2 my-sm-0 position-relative text-center d-flex align-center justify-center">
                    <div class="arrow-line animated-flow"></div>
                    <v-icon class="arrow-tip" color="indigo">mdi-chevron-right</v-icon>
                    <span class="text-caption bg-white px-2 position-absolute" style="font-size: 0.6rem !important; top: -14px;">X-Agent-Token (Private)</span>
                  </div>

                  <!-- Node Center: CQA Gateway Checks -->
                  <div class="graph-node pa-3 text-center rounded-xl elevation-2 bg-white border-2" :style="{ width: '200px', borderColor: '#4caf50' }">
                    <v-icon color="success" size="small" class="mb-1">mdi-shield-check-outline</v-icon>
                    <div class="text-caption font-weight-bold">Gateway (Private Scopes)</div>
                    <div class="mt-1 d-flex justify-center ga-1 flex-wrap">
                      <v-chip size="x-small" :color="erp.privateScopes.includes('read_products') ? 'success' : 'grey-lighten-2'">Prod</v-chip>
                      <v-chip size="x-small" :color="erp.privateScopes.includes('read_inventory') ? 'success' : 'grey-lighten-2'">Stock</v-chip>
                      <v-chip size="x-small" :color="erp.privateScopes.includes('read_orders') ? 'success' : 'grey-lighten-2'">Ord</v-chip>
                      <v-chip size="x-small" :color="erp.privateScopes.includes('read_customers') ? 'success' : 'grey-lighten-2'">Cust</v-chip>
                    </div>
                  </div>

                  <!-- Path Center-to-Right -->
                  <div class="graph-arrow flex-grow-1 mx-2 my-2 my-sm-0 position-relative text-center d-flex align-center justify-center">
                    <div class="arrow-line" :class="{ 'animated-flow-success': erp.privateScopes.length > 0 }"></div>
                    <v-icon class="arrow-tip" :color="erp.privateScopes.length > 0 ? 'success' : 'grey'">mdi-chevron-right</v-icon>
                    <span class="text-caption bg-white px-2 position-absolute" :class="erp.privateScopes.length > 0 ? 'text-success' : 'text-grey'" style="font-size: 0.6rem !important; top: -14px;">
                      {{ erp.privateScopes.length > 0 ? 'Cho phép' : 'Chặn' }}
                    </span>
                  </div>

                  <!-- Node Right: Cloudify ERP -->
                  <div class="graph-node pa-3 text-center rounded-xl elevation-2 bg-white border-2" :style="{ width: '150px', borderColor: erp.privateScopes.length > 0 ? '#ff9800' : '#9e9e9e' }">
                    <v-icon :color="erp.privateScopes.length > 0 ? 'warning' : 'grey'" size="small" class="mb-1">mdi-server-network</v-icon>
                    <div class="text-caption font-weight-bold" :class="{ 'text-grey': erp.privateScopes.length === 0 }">Cloudify ERP</div>
                    <div class="text-grey text-caption" style="font-size: 0.65rem !important" v-if="erp.privateScopes.includes('read_inventory') && erp.privateProductGroups">
                      Filter: {{ erp.privateProductGroups }}
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </v-expand-transition>
        </v-window-item>

      </v-window>

      <!-- Action Save Settings -->
      <div class="d-flex ga-2 mt-4">
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

const activeBotTab = ref('public')

const langflow = reactive({
  baseUrl: '',
  flowId: '',
  publicFlowId: '',
  token: '',
})

const erp = reactive({
  url: '',
  token: '',
  username: '',
  password: '',
  // Public Bot Config
  publicActive: false,
  publicScopes: [] as string[],
  publicProductGroups: '',
  publicAgentToken: '',
  // Private Bot Config
  privateActive: false,
  privateScopes: [] as string[],
  privateProductGroups: '',
  privateAgentToken: '',
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

    erp.url = settings.erp_api_url || ''
    erp.username = settings.erp_api_username || ''
    
    if (settings.erp_api_token) {
      erp.token = settings.erp_api_token
    }
    if (settings.erp_api_password) {
      erp.password = settings.erp_api_password
    }

    // Public Bot
    erp.publicActive = settings.erp_public_active === 'true'
    erp.publicProductGroups = settings.erp_public_product_groups || ''
    erp.publicAgentToken = settings.ai_agent_erp_token_public || ''
    if (settings.erp_public_scopes) {
      erp.publicScopes = settings.erp_public_scopes.split(',').filter(Boolean)
    }

    // Private Bot
    erp.privateActive = settings.erp_private_active === 'true'
    erp.privateProductGroups = settings.erp_private_product_groups || ''
    erp.privateAgentToken = settings.ai_agent_erp_token_private || ''
    if (settings.erp_private_scopes) {
      erp.privateScopes = settings.erp_private_scopes.split(',').filter(Boolean)
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
      url: erp.url,
      token: erp.token,
      username: erp.username,
      password: erp.password,
      // Public bot settings
      public_active: erp.publicActive ? 'true' : 'false',
      public_scopes: erp.publicScopes.join(','),
      public_product_groups: erp.publicProductGroups,
      // Private bot settings
      private_active: erp.privateActive ? 'true' : 'false',
      private_scopes: erp.privateScopes.join(','),
      private_product_groups: erp.privateProductGroups,
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
  transition: all 0.3s ease;
}
.graph-node {
  transition: all 0.3s ease;
  z-index: 2;
}
.graph-node:hover {
  transform: translateY(-2px);
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
