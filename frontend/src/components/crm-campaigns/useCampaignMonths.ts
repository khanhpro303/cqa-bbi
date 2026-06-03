// Shared month-dropdown options for the campaign dashboards (inline overview + per-campaign modal).
// Returns the current month plus the previous 5, as { title, value } where value is 'YYYY-MM'.
import { computed, ref, type ComputedRef, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'

interface MonthOption {
  title: string
  value: string
}

export function useCampaignMonths(): {
  monthOptions: ComputedRef<MonthOption[]>
  month: Ref<string>
} {
  const { t } = useI18n()

  const monthOptions = computed<MonthOption[]>(() => {
    const out: MonthOption[] = []
    const now = new Date()
    for (let i = 0; i < 6; i++) {
      const d = new Date(now.getFullYear(), now.getMonth() - i, 1)
      const value = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}`
      // `cron_at` is 'lúc' in Vietnamese — cheap locale probe to label the option.
      out.push({ title: `${t('cron_at') === 'lúc' ? 'Tháng' : 'Month'} ${value}`, value })
    }
    return out
  })

  const month = ref(monthOptions.value[0]?.value ?? '')

  return { monthOptions, month }
}
