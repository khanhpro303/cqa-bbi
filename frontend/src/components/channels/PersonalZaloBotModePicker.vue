<template>
  <fieldset class="bot-mode-picker border-0" :class="{ 'bot-mode-picker--compact': compact, 'mb-3': !compact }">
    <legend :class="compact ? 'd-sr-only' : 'text-subtitle-2 mb-2'">{{ $t('personal_zalo_bot_mode') }}</legend>
    <v-btn-toggle
      :model-value="mode"
      :disabled="disabled"
      mandatory
      density="compact"
      color="primary"
      variant="text"
      class="bot-mode-toggle"
      :class="{ 'bot-mode-toggle--compact': compact }"
      @update:model-value="mode = $event"
    >
      <v-btn
        value="chatbot"
        rounded="md"
        variant="text"
        class="bot-mode-option"
        :class="{ 'bot-mode-option--active': mode === 'chatbot' }"
        :size="compact ? 'small' : undefined"
        :aria-pressed="mode === 'chatbot'"
      >Chatbot</v-btn>
      <v-btn
        value="internbot"
        rounded="md"
        variant="text"
        class="bot-mode-option"
        :class="{ 'bot-mode-option--active': mode === 'internbot' }"
        :size="compact ? 'small' : undefined"
        :aria-pressed="mode === 'internbot'"
      >Internbot</v-btn>
    </v-btn-toggle>
    <div v-if="!compact" class="text-caption text-medium-emphasis mt-2">
      {{ $t(mode === 'internbot' ? 'personal_zalo_internbot_hint' : 'personal_zalo_chatbot_mode_hint') }}
    </div>
  </fieldset>
</template>

<script setup lang="ts">
import type { PersonalZaloBotMode } from './personalZaloBotSettings'

defineProps<{ disabled?: boolean; compact?: boolean }>()
const mode = defineModel<PersonalZaloBotMode>({ required: true })
</script>

<style scoped>
.bot-mode-picker {
  margin: 0;
  padding: 0;
  min-width: 0;
}

.bot-mode-toggle {
  height: auto !important;
  padding: 3px;
  gap: 3px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 10px !important;
  background: rgba(var(--v-theme-on-surface), 0.035);
  overflow: visible;
}

.bot-mode-toggle--compact {
  padding: 2px;
  gap: 2px;
  border-radius: 8px !important;
}

.bot-mode-toggle :deep(.bot-mode-option) {
  min-width: 74px;
  height: 28px !important;
  padding: 0 12px !important;
  border: 0 !important;
  border-radius: 7px !important;
  font-size: 0.8125rem;
  font-weight: 500;
  line-height: 1;
  letter-spacing: normal;
  text-transform: none;
  color: rgba(var(--v-theme-on-surface), 0.72);
}

.bot-mode-toggle--compact :deep(.bot-mode-option) {
  min-width: 0;
  height: 22px !important;
  padding: 0 7px !important;
  border-radius: 6px !important;
  font-size: 0.71875rem;
}

.bot-mode-toggle :deep(.bot-mode-option--active) {
  color: rgb(var(--v-theme-primary));
  font-weight: 600;
  background: rgb(var(--v-theme-surface));
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.08);
}
</style>
