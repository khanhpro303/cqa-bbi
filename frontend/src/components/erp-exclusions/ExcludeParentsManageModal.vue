<template>
  <v-dialog v-model="dialog" max-width="960" persistent>
    <v-card>
      <v-card-title class="d-flex align-center">
        <v-icon class="mr-2" color="primary">mdi-filter-cog-outline</v-icon>
        Cấu hình mã cha loại trừ
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
            placeholder="Tìm theo mã cha hoặc nhãn hiệu..."
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
          "Chọn/Bỏ chọn tất cả đang thấy" chỉ áp dụng cho danh sách sau khi đã lọc — dùng ô tìm kiếm ở trên để thu hẹp trước.
        </p>

        <v-progress-linear v-if="loading" indeterminate color="primary" class="mb-2" />

        <div v-else-if="items.length === 0" class="text-center text-grey pa-6">
          <v-icon size="48" color="grey-lighten-1">mdi-database-off-outline</v-icon>
          <div class="mt-2 text-body-2">
            Chưa có dữ liệu raw từ ERP. Hãy chạy đồng bộ ERP trước.
          </div>
        </div>

        <div v-else class="exclusion-list">
          <v-virtual-scroll
            :items="filteredItems"
            :item-height="48"
            height="420"
          >
            <template #default="{ item }">
              <div
                :key="item.parent_sku"
                class="d-flex align-center px-3 py-2 row-item"
                :class="{ 'row-selected': selected.has(item.parent_sku) }"
              >
                <v-checkbox-btn
                  :model-value="selected.has(item.parent_sku)"
                  density="compact"
                  hide-details
                  @update:model-value="toggleOne(item.parent_sku, $event)"
                />
                <div class="flex-grow-1 ml-2">
                  <div class="text-body-2 font-weight-medium">
                    {{ item.parent_sku }}
                  </div>
                  <div class="text-caption text-grey">
                    {{ item.nhan_hieu || '—' }} · {{ item.child_count }} SKU con
                  </div>
                </div>
                <v-chip
                  v-if="selected.has(item.parent_sku)"
                  size="x-small"
                  color="warning"
                  variant="tonal"
                >
                  Loại trừ
                </v-chip>
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

export interface ParentSKURow {
  parent_sku: string
  child_count: number
  nhan_hieu: string
  is_excluded: boolean
  updated_at?: string
}

interface Props {
  modelValue: boolean
  tenantId: string
  initialItems: ParentSKURow[]
  loading?: boolean
}
const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
  (e: 'updated', cacheRows: number): void
}>()

const dialog = ref(props.modelValue)
const search = ref('')
const items = ref<ParentSKURow[]>([])
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
    props.initialItems.filter((i) => i.is_excluded).map((i) => i.parent_sku),
  )
  search.value = ''
  errorMsg.value = ''
}

const filteredItems = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return items.value
  return items.value.filter(
    (i) =>
      i.parent_sku.toLowerCase().includes(q) ||
      (i.nhan_hieu || '').toLowerCase().includes(q),
  )
})

function toggleOne(parentSku: string, value: boolean | null): void {
  const next = new Set(selected.value)
  if (value) {
    next.add(parentSku)
  } else {
    next.delete(parentSku)
  }
  selected.value = next
}

function selectAllVisible(): void {
  const next = new Set(selected.value)
  for (const item of filteredItems.value) {
    next.add(item.parent_sku)
  }
  selected.value = next
}

function deselectAllVisible(): void {
  const next = new Set(selected.value)
  for (const item of filteredItems.value) {
    next.delete(item.parent_sku)
  }
  selected.value = next
}

async function save(): Promise<void> {
  saving.value = true
  errorMsg.value = ''
  try {
    const body = { excluded_parent_skus: Array.from(selected.value) }
    const { data } = await api.put<{ excluded_count: number; cache_rows: number }>(
      `/tenants/${props.tenantId}/erp/parent-skus/exclusions`,
      body,
    )
    emit('updated', data.cache_rows)
    dialog.value = false
  } catch (err: unknown) {
    if (err && typeof err === 'object' && 'response' in err) {
      const r = (err as { response?: { data?: { message?: string; error?: string } } }).response
      errorMsg.value = r?.data?.message || r?.data?.error || 'Lưu thất bại.'
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
.row-item {
  border-bottom: 1px solid rgba(0, 0, 0, 0.06);
}
.row-item:last-child {
  border-bottom: none;
}
.row-selected {
  background-color: rgba(255, 152, 0, 0.06);
}
</style>
