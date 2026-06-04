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
        <v-stepper v-model="step" :items="stepLabels" hide-actions flat class="campaign-stepper">
          <!-- Step 1: General -->
          <template #item.1>
            <v-form ref="form1Ref" class="pt-2">
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
            </v-form>
          </template>

          <!-- Step 2: Message -->
          <template #item.2>
            <v-form ref="form2Ref" class="pt-2">
              <MessageComposer ref="composerRef" v-model="form.message" />
            </v-form>
          </template>

          <!-- Step 3: Schedule & segments -->
          <template #item.3>
            <v-form ref="form3Ref" class="pt-2">
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
          </template>
        </v-stepper>
      </v-card-text>

      <v-divider />
      <v-card-actions class="py-3 px-4">
        <v-btn
          v-if="step > 1"
          variant="text"
          prepend-icon="mdi-chevron-left"
          :disabled="!!saving"
          @click="step--"
        >
          {{ $t('back') }}
        </v-btn>
        <v-spacer />
        <v-btn variant="text" :disabled="!!saving" @click="close">{{ $t('cancel') }}</v-btn>
        <v-btn v-if="step < 3" color="primary" append-icon="mdi-chevron-right" @click="next">
          {{ $t('next') }}
        </v-btn>
        <template v-else>
          <v-btn variant="tonal" color="blue-grey" :loading="saving === 'draft'" @click="submit('draft')">
            {{ $t('campaign_save_draft') }}
          </v-btn>
          <v-btn color="primary" :loading="saving === 'active'" @click="submit('active')">
            {{ $t('campaign_activate') }}
          </v-btn>
        </template>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import MessageComposer from './MessageComposer.vue'
import SegmentScheduleRow from './SegmentScheduleRow.vue'
import { saveCampaign, setCampaignStatus } from './campaignsApi'
import type { Campaign, CampaignFormState, CampaignSegment, SelectOption } from './types'

const { t } = useI18n()

const props = defineProps<{
  modelValue: boolean
  groups: SelectOption[]
  channels: SelectOption[]
  editing?: Campaign | null
}>()

const emit = defineEmits<{
  'update:modelValue': [boolean]
  // Second arg is an optional i18n warning key (e.g. saved but channel disabled),
  // letting the parent show a warning toast instead of the default success one.
  saved: [Campaign, string?]
  error: [string]
}>()

const open = ref(props.modelValue)
watch(() => props.modelValue, (v) => { open.value = v; if (v) resetForm() })
watch(open, (v) => emit('update:modelValue', v))

const step = ref(1)
const form1Ref = ref<any>(null)
const form2Ref = ref<any>(null)
const form3Ref = ref<any>(null)
const composerRef = ref<{ commit: () => Promise<void> } | null>(null)
const saving = ref<false | 'draft' | 'active'>(false)

const stepLabels = computed(() => [
  t('campaign_section_general'),
  t('campaign_section_message'),
  t('campaign_section_schedule'),
])

function emptyForm(): CampaignFormState {
  return {
    name: '',
    description: '',
    channelId: props.channels[0]?.id ?? '',
    message: {
      text: '',
      link: undefined,
      images: [],
      reminderText: undefined,
      mentionPlacement: 'prefix',
      mentionGreeting: undefined,
    },
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
    mentionMode: 'none',
    mentionUserIds: [],
  }
}

function resetForm() {
  step.value = 1
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

// Validate the current step before advancing; gate forward navigation only.
async function next() {
  if (step.value === 1) {
    const { valid } = (await form1Ref.value?.validate()) || { valid: false }
    if (valid) step.value = 2
    return
  }
  if (step.value === 2) {
    const { valid } = (await form2Ref.value?.validate()) || { valid: false }
    // Fallback guard in case the composer's required rule is absent.
    if (!valid || !form.value.message.text.trim()) return
    step.value = 3
  }
}

async function submit(target: 'draft' | 'active') {
  const { valid } = (await form3Ref.value?.validate()) || { valid: false }
  if (!valid) return
  if (form.value.segments.length === 0) {
    emit('error', 'campaign_no_segment_error')
    return
  }
  saving.value = target
  try {
    // Upload any pending images first, then persist the campaign with their paths.
    try {
      await composerRef.value?.commit()
    } catch (err) {
      // Surface the real cause (HTTP status + backend error body, or a 413 from
      // the proxy) so upload failures are diagnosable instead of swallowed.
      console.error('[crm-campaign] image upload failed', err)
      emit('error', 'campaign_image_upload_error')
      return
    }
    let saved = await saveCampaign(form.value)

    // Editing an active campaign onto a missing/disabled channel auto-pauses it
    // server-side (UpdateCampaign). Surface why instead of a silent status flip.
    if (props.editing?.status === 'active' && saved.status === 'paused') {
      emit('saved', saved, 'campaign_autopaused_channel_inactive')
      open.value = false
      return
    }

    if (target === 'active') {
      try {
        saved = (await setCampaignStatus(saved.id, 'active')) ?? saved
      } catch (e: any) {
        // The campaign is already persisted (as draft) — only activation failed
        // because the channel is off. Refresh the list and tell the user why.
        const code = e?.response?.data?.error
        if (code === 'channel_inactive' || code === 'channel_not_found') {
          emit('saved', saved, 'campaign_saved_but_channel_inactive')
          open.value = false
          return
        }
        throw e
      }
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
/* Stepper sits inside the scrollable card body — drop the elevated card chrome. */
.campaign-stepper {
  background: transparent;
}
.campaign-stepper :deep(.v-stepper-header) {
  box-shadow: none;
  margin-bottom: 4px;
}
.campaign-stepper :deep(.v-stepper-window) {
  margin: 0;
}
</style>
