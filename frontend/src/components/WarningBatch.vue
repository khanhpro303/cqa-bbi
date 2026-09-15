<template>
  <v-alert v-if="warnings.length" type="warning" variant="tonal" border="start">
    <button v-if="warnings.length > 1" class="warning-toggle" type="button" :aria-expanded="expanded" @click="expanded = !expanded">
      <strong>Có {{ warnings.length }} cảnh báo</strong>
      <v-icon :icon="expanded ? 'mdi-chevron-up' : 'mdi-chevron-down'" />
    </button>
    <div v-if="warnings.length === 1 || expanded" :class="{ 'mt-3': warnings.length > 1 }">
      <div v-for="(warning, index) in warnings" :key="warning.id" :class="{ 'mt-3 pt-3 warning-divider': index > 0 }">
        <div class="d-flex align-center justify-space-between flex-wrap ga-3">
          <div>
            <strong>{{ warning.title }}</strong>
            <div class="text-body-2 mt-1">{{ warning.detail }}</div>
          </div>
          <slot name="action" :warning="warning" />
        </div>
      </div>
    </div>
  </v-alert>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'

const props = defineProps<{ warnings: { id: string; title: string; detail: string }[] }>()
const expanded = ref(false)

watch(() => props.warnings.map(warning => warning.id).join(','), () => {
  expanded.value = false
})
</script>

<style scoped>
.warning-toggle {
  display: flex;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.warning-divider {
  border-top: 1px solid currentColor;
}
</style>
