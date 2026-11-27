<template>
  <fieldset class="border-0 mb-3">
    <legend class="text-subtitle-2 mb-2">{{ $t('personal_zalo_bot_mode') }}</legend>
    <v-btn-toggle
      :model-value="mode"
      :disabled="disabled"
      mandatory
      density="compact"
      color="primary"
      variant="outlined"
      @update:model-value="mode = $event"
    >
      <v-btn value="chatbot" :aria-pressed="mode === 'chatbot'">Chatbot</v-btn>
      <v-btn value="internbot" :aria-pressed="mode === 'internbot'">Internbot</v-btn>
    </v-btn-toggle>
    <div class="text-caption text-medium-emphasis mt-2">
      {{ $t(mode === 'internbot' ? 'personal_zalo_internbot_hint' : 'personal_zalo_chatbot_mode_hint') }}
    </div>
  </fieldset>
</template>

<script setup lang="ts">
import type { PersonalZaloBotMode } from './personalZaloBotSettings'

defineProps<{ disabled?: boolean }>()
const mode = defineModel<PersonalZaloBotMode>({ required: true })
</script>
