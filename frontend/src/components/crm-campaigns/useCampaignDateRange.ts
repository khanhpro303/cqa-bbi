// Shared time filter for the campaign dashboards (inline overview + per-campaign modal).
// Mirrors the home dashboard (Dashboard.vue): preset chips + a free from/to date range.
import { ref, type Ref } from 'vue'

export interface DatePreset {
  label: string
  value: string
}

// Same options as the home dashboard ("Tổng quan" filter).
export const CAMPAIGN_DATE_PRESETS: DatePreset[] = [
  { label: 'Hôm nay', value: 'today' },
  { label: '7 ngày', value: '7days' },
  { label: '28 ngày', value: '28days' },
  { label: 'Tháng này', value: 'month' },
  { label: 'Quý này', value: 'quarter' },
  { label: 'Năm này', value: 'year' },
]

const DEFAULT_PRESET = '28days'

function formatDate(d: Date): string {
  return d.toISOString().split('T')[0]
}

export interface CampaignDateRange {
  dateFrom: Ref<string>
  dateTo: Ref<string>
  datePreset: Ref<string>
  presets: DatePreset[]
  applyPreset: (preset: string) => void
  setFrom: (value: string) => void
  setTo: (value: string) => void
}

export function useCampaignDateRange(): CampaignDateRange {
  const now = new Date()
  const dateFrom = ref(formatDate(new Date(now.getFullYear(), now.getMonth(), now.getDate() - 28)))
  const dateTo = ref(formatDate(now))
  const datePreset = ref(DEFAULT_PRESET)

  function applyPreset(preset: string): void {
    const d = new Date()
    const y = d.getFullYear()
    const m = d.getMonth()
    datePreset.value = preset
    dateTo.value = formatDate(d)

    switch (preset) {
      case 'today':
        dateFrom.value = formatDate(new Date(y, m, d.getDate()))
        break
      case '7days':
        dateFrom.value = formatDate(new Date(y, m, d.getDate() - 7))
        break
      case '28days':
        dateFrom.value = formatDate(new Date(y, m, d.getDate() - 28))
        break
      case 'month':
        dateFrom.value = formatDate(new Date(y, m, 1))
        break
      case 'quarter': {
        const qm = Math.floor(m / 3) * 3
        dateFrom.value = formatDate(new Date(y, qm, 1))
        break
      }
      case 'year':
        dateFrom.value = formatDate(new Date(y, 0, 1))
        break
    }
  }

  // Manual date edits clear the active preset chip (custom range).
  function setFrom(value: string): void {
    dateFrom.value = value
    datePreset.value = ''
  }

  function setTo(value: string): void {
    dateTo.value = value
    datePreset.value = ''
  }

  return { dateFrom, dateTo, datePreset, presets: CAMPAIGN_DATE_PRESETS, applyPreset, setFrom, setTo }
}
