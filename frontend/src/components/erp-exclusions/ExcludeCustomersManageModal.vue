<template>
  <v-dialog v-model="dialog" max-width="960" persistent>
    <v-card>
      <v-card-title class="d-flex align-center">
        <v-icon class="mr-2" color="primary">mdi-account-cancel-outline</v-icon>
        Cấu hình mã khách hàng loại trừ
        <v-spacer />
        <v-btn
          icon="mdi-close"
          variant="text"
          size="small"
          :disabled="saving"
          @click="close"
        />
      </v-card-title>

      <v-card-text class="pb-2">
        <div class="d-flex align-center flex-wrap ga-2 mb-3">
          <v-text-field
            v-model="search"
            density="compact"
            variant="outlined"
            hide-details
            prepend-inner-icon="mdi-magnify"
            placeholder="Tìm theo mã KH, tên hoặc SĐT..."
            clearable
            style="max-width: 360px;"
          />
          <v-chip size="small" variant="tonal" color="info">
            Hiển thị: {{ filteredItems.length }} / {{ items.length }}
          </v-chip>
          <v-chip size="small" variant="tonal" color="warning">
            Đang loại: {{ selected.size }}
          </v-chip>
          <v-spacer />
          <v-btn
            size="small"
            variant="outlined"
            prepend-icon="mdi-checkbox-multiple-marked-outline"
            :disabled="filteredItems.length === 0"
            @click="selectAllVisible"
          >
            Chọn tất cả đang thấy
          </v-btn>
          <v-btn
            size="small"
            variant="outlined"
            prepend-icon="mdi-checkbox-multiple-blank-outline"
            :disabled="filteredItems.length === 0"
            @click="deselectAllVisible"
          >
            Bỏ chọn tất cả đang thấy
          </v-btn>
        </div>

        <p class="text-caption text-grey mb-2">
          Mã khách hàng đã link group GMF bị khoá — không thể loại trừ vì cache là nguồn phân quyền của group.
        </p>

        <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-2" />

        <div v-else-if="items.length === 0" class="text-center text-grey pa-6">
          <v-icon size="48" color="grey-lighten-1">mdi-database-off-outline</v-icon>
          <div class="mt-2 text-body-2">
            Chưa có khách hàng nào trong cache. Hãy chạy đồng bộ ERP trước.
          </div>
        </div>

        <div v-else class="exclusion-list">
          <v-virtual-scroll
            :items="chunkedItems"
            :item-height="48"
            height="420"
          >
            <template #default="{ item: pair }">
              <div class="d-flex w-100 scroll-row">
                <template v-for="(slot, idx) in [0, 1]" :key="idx">
                  <div
                    v-if="pair[slot]"
                    class="d-flex align-center px-3 py-2 col-item"
                    :class="{
                      'row-selected': selected.has(pair[slot].customer_code),
                      'border-l': slot === 1,
                      'row-locked': pair[slot].is_gmf_linked,
                    }"
                    :style="{ cursor: pair[slot].is_gmf_linked ? 'not-allowed' : 'pointer', textAlign: 'left' }"
                    @click="onRowClick(pair[slot])"
                  >
                    <v-tooltip
                      v-if="pair[slot].is_gmf_linked"
                      text="Đã link group GMF — không thể loại trừ"
                      location="top"
                    >
                      <template #activator="{ props: tip }">
                        <v-icon v-bind="tip" size="small" color="grey">mdi-lock-outline</v-icon>
                      </template>
                    </v-tooltip>
                    <v-checkbox-btn
                      v-else
                      :model-value="selected.has(pair[slot].customer_code)"
                      density="compact"
                      hide-details
                      @click.stop
                      @update:model-value="toggleOne(pair[slot].customer_code, $event)"
                    />
                    <div class="flex-grow-1 ml-2 text-start" style="text-align: left;">
                      <div class="text-body-2 font-weight-medium">
                        {{ pair[slot].customer_code }}
                      </div>
                      <div class="text-caption text-grey">
                        {{ pair[slot].ho_va_ten || '—' }}<span v-if="pair[slot].dien_thoai"> · {{ pair[slot].dien_thoai }}</span>
                      </div>
                    </div>
                    <v-chip
                      v-if="pair[slot].is_gmf_linked"
                      size="x-small"
                      color="info"
                      variant="tonal"
                      class="ml-2"
                    >
                      GMF
                    </v-chip>
                    <v-chip
                      v-else-if="selected.has(pair[slot].customer_code)"
                      size="x-small"
                      color="warning"
                      variant="tonal"
                      class="ml-2"
                    >
                      Loại trừ
                    </v-chip>
                  </div>
                  <div v-else class="col-item flex-grow-1" :class="{ 'border-l': slot === 1 }" style="flex: 1;"></div>
                </template>
              </div>
            </template>
          </v-virtual-scroll>
        </div>

        <v-alert
          v-if="errorMsg"
          type="error"
          variant="tonal"
          density="comfortable"
          class="mt-3 text-body-2"
        >
          {{ errorMsg }}
        </v-alert>
      </v-card-text>

      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" :disabled="saving" @click="close">Hủy</v-btn>
        <v-btn
          color="primary"
          variant="flat"
          :loading="saving"
          @click="save"
        >
          Lưu ({{ selected.size }} mã)
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import api from '../../api'

export interface CustomerCodeRow {
  customer_code: string
  ho_va_ten: string
  dien_thoai: string
  is_excluded: boolean
  is_gmf_linked: boolean
  updated_at?: string
}

interface Props {
  modelValue: boolean
  tenantId: string
  initialItems: CustomerCodeRow[]
  loading?: boolean
}
const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'updated', cacheRows: number): void
}>()

const dialog = ref(props.modelValue)
const search = ref('')
const items = ref<CustomerCodeRow[]>([])
const selected = ref<Set<string>>(new Set())
const saving = ref(false)
const errorMsg = ref('')

watch(
  () => props.modelValue,
  (v) => {
    dialog.value = v
    if (v) hydrate()
  },
)

watch(dialog, (v) => {
  if (v !== props.modelValue) emit('update:modelValue', v)
})

function hydrate(): void {
  items.value = [...props.initialItems]
  selected.value = new Set(
    props.initialItems
      .filter((i) => i.is_excluded && !i.is_gmf_linked)
      .map((i) => i.customer_code),
  )
  search.value = ''
  errorMsg.value = ''
}

const filteredItems = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return items.value
  return items.value.filter(
    (i) =>
      i.customer_code.toLowerCase().includes(q) ||
      (i.ho_va_ten || '').toLowerCase().includes(q) ||
      (i.dien_thoai || '').toLowerCase().includes(q),
  )
})

const chunkedItems = computed(() => {
  const chunks: CustomerCodeRow[][] = []
  for (let i = 0; i < filteredItems.value.length; i += 2) {
    const chunk: CustomerCodeRow[] = []
    if (filteredItems.value[i]) chunk.push(filteredItems.value[i])
    if (filteredItems.value[i + 1]) chunk.push(filteredItems.value[i + 1])
    chunks.push(chunk)
  }
  return chunks
})

function onRowClick(row: CustomerCodeRow): void {
  if (row.is_gmf_linked) return
  toggleOne(row.customer_code, !selected.value.has(row.customer_code))
}

function toggleOne(code: string, value: boolean | null): void {
  const next = new Set(selected.value)
  if (value) {
    next.add(code)
  } else {
    next.delete(code)
  }
  selected.value = next
}

function selectAllVisible(): void {
  const next = new Set(selected.value)
  for (const item of filteredItems.value) {
    if (!item.is_gmf_linked) next.add(item.customer_code)
  }
  selected.value = next
}

function deselectAllVisible(): void {
  const next = new Set(selected.value)
  for (const item of filteredItems.value) {
    next.delete(item.customer_code)
  }
  selected.value = next
}

async function save(): Promise<void> {
  saving.value = true
  errorMsg.value = ''
  try {
    const body = { excluded_customer_codes: Array.from(selected.value) }
    const { data } = await api.put<{ excluded_count: number; cache_rows: number }>(
      `/tenants/${props.tenantId}/erp/customer-codes/exclusions`,
      body,
    )
    emit('updated', data.cache_rows)
    dialog.value = false
  } catch (err: unknown) {
    if (err && typeof err === 'object' && 'response' in err) {
      const r = (err as { response?: { data?: { message?: string; error?: string; codes?: string[] } } }).response
      const codes = r?.data?.codes
      if (r?.data?.error === 'gmf_linked_blocked' && codes?.length) {
        errorMsg.value = `Không thể loại trừ mã đã link group GMF: ${codes.join(', ')}`
      } else {
        errorMsg.value = r?.data?.message || r?.data?.error || 'Lưu thất bại.'
      }
    } else {
      errorMsg.value = 'Lưu thất bại.'
    }
  } finally {
    saving.value = false
  }
}

function close(): void {
  dialog.value = false
}
</script>

<style scoped>
.exclusion-list {
  border: 1px solid rgba(0, 0, 0, 0.12);
  border-radius: 4px;
  overflow: hidden;
}
.scroll-row {
  border-bottom: 1px solid rgba(0, 0, 0, 0.06);
}
.scroll-row:last-child {
  border-bottom: none;
}
.col-item {
  flex: 1;
  width: 50%;
  transition: background-color 0.2s;
}
.col-item:hover {
  background-color: rgba(0, 0, 0, 0.02);
}
.border-l {
  border-left: 1px solid rgba(0, 0, 0, 0.06);
}
.row-selected {
  background-color: rgba(255, 152, 0, 0.06);
}
.row-selected:hover {
  background-color: rgba(255, 152, 0, 0.1);
}
.row-locked {
  background-color: rgba(0, 0, 0, 0.03);
}
.row-locked:hover {
  background-color: rgba(0, 0, 0, 0.03);
}
</style>
