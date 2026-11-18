<template>
  <v-table density="comfortable" hover>
    <thead>
      <tr>
        <th>{{ $t('cm_time') }}</th>
        <th v-if="!compact">{{ $t('cm_customer') }}</th>
        <th>{{ $t('cm_summary') }}</th>
        <th>{{ $t('cm_issues') }}</th>
        <th>{{ $t('cm_status') }}</th>
      </tr>
    </thead>
    <tbody>
      <tr v-for="s in sessions" :key="s.id" class="cursor-pointer" :data-testid="`session-${s.id}`" @click="$emit('open', s)">
        <td class="text-no-wrap">{{ formatDateTime(s.last_activity_at) }}</td>
        <td v-if="!compact">
          <div class="font-weight-medium">{{ s.customer_name || s.subject_id }}</div>
          <div v-if="s.customer_code" class="text-caption text-medium-emphasis">{{ s.customer_code }}</div>
        </td>
        <td class="summary-cell">{{ s.summary || '—' }}</td>
        <td>
          <v-chip
            v-if="parseIssues(s.issues).length"
            size="x-small"
            :color="severityColor(highestSeverity(parseIssues(s.issues)))"
          >
            {{ parseIssues(s.issues).length }} · {{ $t(`cm_severity_${highestSeverity(parseIssues(s.issues))}`) }}
          </v-chip>
          <v-icon v-if="s.alerted_at" size="small" color="warning" class="ml-1" :title="$t('cm_alerted_at')">mdi-bell-ring</v-icon>
        </td>
        <td>{{ $t(`cm_status_${s.status}`) }}</td>
      </tr>
      <tr v-if="!loading && sessions.length === 0">
        <td :colspan="compact ? 4 : 5" class="text-center text-medium-emphasis py-6">{{ $t('cm_no_sessions') }}</td>
      </tr>
    </tbody>
  </v-table>
</template>

<script setup lang="ts">
import { type ChatSession, parseIssues, highestSeverity, severityColor } from './labels'

defineProps<{ sessions: ChatSession[]; loading?: boolean; compact?: boolean }>()
defineEmits<{ (e: 'open', s: ChatSession): void }>()

function formatDateTime(v?: string | null) {
  if (!v) return '—'
  return new Date(v).toLocaleString('vi-VN', { hour: '2-digit', minute: '2-digit', day: '2-digit', month: '2-digit' })
}
</script>

<style scoped>
.summary-cell {
  max-width: 480px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.cursor-pointer { cursor: pointer; }
</style>
