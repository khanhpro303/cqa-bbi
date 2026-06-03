<template>
  <v-dialog v-model="dialog" max-width="500">
    <v-card>
      <v-card-title class="d-flex align-center text-warning">
        <v-icon class="mr-2" color="warning">mdi-alert-outline</v-icon>
        {{ t('erp_group_filter_required_title') }}
      </v-card-title>

      <v-card-text>
        <v-alert type="warning" variant="tonal" density="comfortable" class="mb-3 text-body-2">
          {{ t('erp_group_filter_required_desc') }}
        </v-alert>
        <ul class="pl-4">
          <li
            v-for="resource in resources"
            :key="resource"
            class="text-body-2 font-weight-medium"
          >
            {{ t(`erp_resource_${resource}`) }}
          </li>
        </ul>
      </v-card-text>

      <v-card-actions>
        <v-spacer />
        <v-btn color="warning" variant="flat" @click="dialog = false">
          {{ t('understood') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

interface Props {
  modelValue: boolean
  resources: string[]
}
const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: boolean): void
}>()

const { t } = useI18n()

const dialog = computed({
  get: () => props.modelValue,
  set: (value: boolean) => emit('update:modelValue', value),
})
</script>
