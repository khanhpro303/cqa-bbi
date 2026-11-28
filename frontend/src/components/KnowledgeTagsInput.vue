<template>
  <v-combobox
    :model-value="modelValue"
    :items="suggestions"
    :label="$t('kb_tags_label')"
    :hint="$t('kb_tags_hint')"
    :disabled="disabled"
    :delimiters="[',']"
    persistent-hint
    multiple
    chips
    closable-chips
    clearable
    density="compact"
    :variant="variant"
    data-testid="kb-tags-input"
    @update:model-value="update"
  />
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = withDefaults(defineProps<{
  modelValue: string[]
  known?: string[]
  disabled?: boolean
  variant?: 'outlined' | 'filled' | 'underlined' | 'plain' | 'solo' | 'solo-inverted' | 'solo-filled'
}>(), { known: () => [], disabled: false, variant: 'filled' })
const emit = defineEmits<{ 'update:modelValue': [tags: string[]] }>()

const suggestions = computed(() => props.known.filter((tag) => !props.modelValue.includes(tag)))

// Same normal form as the server (knowledge.NormalizeTag), so a tag typed
// with different case or spacing does not appear twice.
function update(value: unknown) {
  const list = Array.isArray(value) ? value : []
  const out: string[] = []
  for (const raw of list) {
    const tag = normalizeTag(String(raw ?? ''))
    if (tag && !out.includes(tag)) out.push(tag)
  }
  emit('update:modelValue', out)
}
</script>

<script lang="ts">
export function normalizeTag(tag: string): string {
  return tag.normalize('NFC').trim().split(/\s+/).join(' ').toLowerCase()
}
</script>
