<template>
  <v-dialog v-model="open" max-width="860" scrollable persistent>
    <v-card>
      <v-card-title class="d-flex align-center py-3">
        <v-icon color="primary" class="mr-2">mdi-bullhorn-outline</v-icon>
        {{ form.id ? $t('campaign_edit') : $t('campaign_create') }}
        <v-spacer />
        <v-btn icon="mdi-close" variant="text" size="small" @click="close" />
      </v-card-title>
      <v-divider />

      <v-card-text style="max-height: 72vh">
        <v-form ref="formRef">
          <!-- Section 1: General -->
          <div class="section-label">
            <v-icon size="18" class="mr-1">mdi-information-outline</v-icon>
            {{ $t('campaign_section_general') }}
          </div>
          <v-text-field
            v-model="form.name"
            :label="$t('campaign_name') + ' *'"
            variant="outlined"
            density="comfortable"
            :rules="[v => !!(v && v.trim()) || $t('required')]"
            class="mb-1"
          />
          <v-text-field
            v-model="form.description"
            :label="$t('campaign_desc')"
            variant="outlined"
            density="comfortable"
            class="mb-1"
          />
          <v-select
            v-model="form.channelId"
            :items="channels"
            item-title="name"
            item-value="id"
            :label="$t('campaign_channel') + ' *'"
            variant="outlined"
            density="comfortable"
            prepend-inner-icon="mdi-message-text"
            :rules="[v => !!v || $t('required')]"
          />

          <!-- Section 2: Message -->
          <div class="section-label mt-4">
            <v-icon size="18" class="mr-1">mdi-message-text-outline</v-icon>
            {{ $t('campaign_section_message') }}
          </div>
          <MessageComposer v-model="form.message" />

          <!-- Section 3: Schedule & segments -->
          <div class="section-label mt-5">
            <v-icon size="18" class="mr-1">mdi-calendar-clock</v-icon>
            {{ $t('campaign_section_schedule') }}
          </div>
          <p class="text-caption text-grey-darken-1 mb-3">{{ $t('campaign_schedule_hint') }}</p>

          <SegmentScheduleRow
            v-for="(seg, i) in form.segments"
            :key="seg.id"
            v-model="form.segments[i]"
            :index="i"
            :groups="groups"
            @remove="removeSegment(i)"
          />

          <v-alert
            v-if="form.segments.length === 0"
            type="info"
            variant="tonal"
            density="compact"
            class="mb-3 text-body-2"
          >
            {{ $t('campaign_no_segment') }}
          </v-alert>

          <v-btn
            variant="tonal"
            color="primary"
            prepend-icon="mdi-plus"
            size="small"
            @click="addSegment"
          >
            {{ $t('campaign_add_segment') }}
          </v-btn>
        </v-form>
      </v-card-text>

      <v-divider />
      <v-card-actions class="py-3 px-4">
        <v-spacer />
        <v-btn variant="text" :disabled="!!saving" @click="close">{{ $t('cancel') }}</v-btn>
        <v-btn variant="tonal" color="blue-grey" :loading="saving === 'draft'" @click="submit('draft')">
          {{ $t('campaign_save_draft') }}
        </v-btn>
        <v-btn color="primary" :loading="saving === 'active'" @click="submit('active')">
          {{ $t('campaign_activate') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import MessageComposer from './MessageComposer.vue'
import SegmentScheduleRow from './SegmentScheduleRow.vue'
import { saveCampaign, setCampaignStatus } from './campaignsApi'
import type { Campaign, CampaignFormState, CampaignSegment, SelectOption } from './types'

const props = defineProps<{
  modelValue: boolean
  groups: SelectOption[]
  channels: SelectOption[]
  editing?: Campaign | null
}>()

const emit = defineEmits<{
  'update:modelValue': [boolean]
  saved: [Campaign]
  error: [string]
}>()

const open = ref(props.modelValue)
watch(() => props.modelValue, (v) => { open.value = v; if (v) resetForm() })
watch(open, (v) => emit('update:modelValue', v))

const formRef = ref<any>(null)
const saving = ref<false | 'draft' | 'active'>(false)

function emptyForm(): CampaignFormState {
  return {
    name: '',
    description: '',
    channelId: props.channels[0]?.id ?? '',
    message: { text: '', link: undefined, imageName: undefined },
    segments: [],
  }
}

const form = ref<CampaignFormState>(emptyForm())

function newSegment(): CampaignSegment {
  return {
    id: `seg_${Math.random().toString(36).slice(2, 9)}`,
    groupId: '',
    groupName: '',
    scheduleKind: 'recurring',
    cron: '0 9 * * *',
  }
}

function resetForm() {
  if (props.editing) {
    // Deep-ish clone so edits stay local until saved.
    form.value = {
      id: props.editing.id,
      name: props.editing.name,
      description: props.editing.description ?? '',
      channelId: props.editing.channelId,
      message: { ...props.editing.message },
      segments: props.editing.segments.map((s) => ({ ...s })),
    }
  } else {
    form.value = emptyForm()
    form.value.segments = [newSegment()]
  }
}

function addSegment() {
  form.value = { ...form.value, segments: [...form.value.segments, newSegment()] }
}

function removeSegment(i: number) {
  form.value = {
    ...form.value,
    segments: form.value.segments.filter((_, idx) => idx !== i),
  }
}

async function submit(target: 'draft' | 'active') {
  const { valid } = (await formRef.value?.validate()) || { valid: false }
  if (!valid) return
  if (form.value.segments.length === 0) {
    emit('error', 'campaign_no_segment_error')
    return
  }
  saving.value = target
  try {
    let saved = await saveCampaign(form.value)
    if (target === 'active') {
      saved = (await setCampaignStatus(saved.id, 'active')) ?? saved
    }
    emit('saved', saved)
    open.value = false
  } catch {
    emit('error', 'campaign_save_error')
  } finally {
    saving.value = false
  }
}

function close() {
  open.value = false
}
</script>

<style scoped>
.section-label {
  display: flex;
  align-items: center;
  font-weight: 700;
  font-size: 0.95rem;
  color: rgb(var(--v-theme-primary));
  margin-bottom: 10px;
}
</style>
