<template>
  <!-- Zalo OA always displays the official Zalo brand mark as its placeholder -->
  <v-avatar
    v-if="isZaloOa"
    :size="size"
    rounded="lg"
    v-bind="$attrs"
  >
    <v-img
      :src="ZALO_LOGO_URL"
      :alt="altText"
      cover
      eager
    />
  </v-avatar>

  <!-- Personal Zalo renders the linked account's avatar when valid, falling back to an icon -->
  <v-avatar
    v-else-if="canRenderPersonalAvatar"
    :size="size"
    v-bind="$attrs"
  >
    <v-img
      :key="resolvedAvatarUrl"
      :src="resolvedAvatarUrl"
      :alt="altText"
      cover
      eager
      @error="markAvatarFailed"
    >
      <template #placeholder>
        <v-icon :color="placeholderIconColor" :size="placeholderIconSize">
          mdi-cellphone-link
        </v-icon>
      </template>
    </v-img>
  </v-avatar>

  <v-icon
    v-else-if="variant === 'bare'"
    :color="bareIconColor"
    :size="size"
    v-bind="$attrs"
  >
    {{ bareIconName }}
  </v-icon>

  <v-avatar
    v-else
    :color="channelTypeColor(channelType)"
    :size="size"
    v-bind="$attrs"
  >
    <v-icon color="white" :size="badgeIconSize">
      {{ channelTypeIcon(channelType) }}
    </v-icon>
  </v-avatar>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import {
  ZALO_LOGO_URL,
  avatarFailureKey,
  channelTypeColor,
  channelTypeIcon,
  isSupportedAvatarUrl,
  isZaloOaChannel,
  normalizeAvatarUrl,
} from '../../utils/channel'

defineOptions({ inheritAttrs: false })

const props = withDefaults(defineProps<{
  channelType: string
  channelId?: string
  avatarUrl?: string | null
  name?: string
  size?: string | number
  variant?: 'bare' | 'badge'
}>(), {
  channelId: '',
  avatarUrl: '',
  name: '',
  size: '32',
  variant: 'badge',
})

const failedAvatarKeys = ref<Record<string, boolean>>({})

const isZaloOa = computed(() => isZaloOaChannel(props.channelType))
const resolvedAvatarUrl = computed(() => normalizeAvatarUrl(props.avatarUrl))
const failureKey = computed(() => avatarFailureKey(props.channelId, resolvedAvatarUrl.value))

const canRenderPersonalAvatar = computed(() => {
  if (props.channelType !== 'personal_zalo_import') return false
  if (!resolvedAvatarUrl.value || !isSupportedAvatarUrl(resolvedAvatarUrl.value)) return false
  return !failedAvatarKeys.value[failureKey.value]
})

function markAvatarFailed() {
  if (!resolvedAvatarUrl.value) return
  failedAvatarKeys.value[failureKey.value] = true
}

const altText = computed(() => {
  if (props.name) return props.name
  if (isZaloOa.value) return 'Zalo OA'
  if (props.channelType === 'personal_zalo_import') return 'Zalo cá nhân'
  return props.channelType
})

const numericSize = computed(() => {
  const parsed = Number(props.size)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 32
})

const badgeIconSize = computed(() => Math.max(14, Math.round(numericSize.value / 2)))
const placeholderIconSize = computed(() => (props.variant === 'bare' ? props.size : badgeIconSize.value))
const placeholderIconColor = computed(() => (props.variant === 'bare' ? 'teal' : 'teal'))

const bareIconName = computed(() => {
  if (props.channelType === 'facebook') return 'mdi-facebook-messenger'
  return 'mdi-cellphone-link'
})

const bareIconColor = computed(() => {
  if (props.channelType === 'facebook') return 'indigo'
  return 'teal'
})
</script>
