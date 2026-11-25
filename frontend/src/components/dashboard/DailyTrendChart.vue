<template>
  <div class="dashboard-trend">
    <Line :data="data" :options="options" :aria-label="label" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { useTheme } from 'vuetify'
import { Line } from 'vue-chartjs'
import {
  Chart as ChartJS, CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Filler, Legend,
  type ChartData, type ChartOptions,
} from 'chart.js'
import { formatDashboardDate } from './chartDate'

ChartJS.register(CategoryScale, LinearScale, PointElement, LineElement, Tooltip, Filler, Legend)

const props = defineProps<{
  label: string
  dates: string[]
  series: { label: string; values: number[] }[]
  currency?: boolean
}>()

const { locale } = useI18n()
const theme = useTheme()
const isDark = computed(() => theme.global.current.value.dark)
const axisText = computed(() => isDark.value ? '#c2c6d0' : '#566171')
const data = computed<ChartData<'line'>>(() => {
  const colors = props.currency
    ? [isDark.value ? '#ffb463' : '#bf5b14']
    : isDark.value ? ['#90b4ff', '#52d5b5', '#ffb463'] : ['#365bc4', '#087f70', '#bf5b14']

  return {
    labels: props.dates.map(date => formatDashboardDate(date)),
    datasets: props.series.map((series, index) => {
      const color = colors[index % colors.length]
      return {
        label: series.label,
        data: series.values,
        borderColor: color,
        backgroundColor: props.currency ? `${color}24` : color,
        pointBackgroundColor: color,
        pointBorderWidth: 0,
        borderWidth: 2,
        borderDash: index === 1 ? [5, 3] : [],
        cubicInterpolationMode: 'monotone',
        fill: props.currency,
        pointRadius: props.dates.length <= 7 ? 3 : 0,
        pointHoverRadius: 4,
      }
    }),
  }
})

const options = computed<ChartOptions<'line'>>(() => {
  const compact = new Intl.NumberFormat(locale.value, {
    notation: 'compact', compactDisplay: 'long', maximumFractionDigits: 1,
  })
  const exact = new Intl.NumberFormat(locale.value, props.currency ? { style: 'currency', currency: 'VND' } : {})

  return {
    responsive: true,
    maintainAspectRatio: false,
    animation: false,
    interaction: { mode: 'index', axis: 'x', intersect: false },
    layout: { padding: { right: 6 } },
    plugins: {
      legend: {
        display: props.series.length > 1,
        position: 'top',
        align: 'start',
        labels: { color: axisText.value, boxWidth: 18, boxHeight: 2, padding: 14, font: { size: 12 } },
      },
      tooltip: {
        backgroundColor: '#192332',
        padding: 10,
        caretPadding: 5,
        bodySpacing: 5,
        displayColors: props.series.length > 1,
        callbacks: {
          title: ([item]) => item ? formatDashboardDate(props.dates[item.dataIndex], true) : '',
          label: item => `${item.dataset.label}: ${exact.format(item.parsed.y ?? 0)}`,
        },
      },
    },
    scales: {
      x: {
        offset: true,
        grid: { display: false },
        border: { display: false },
        ticks: {
          autoSkip: true, maxRotation: 0, maxTicksLimit: 7, autoSkipPadding: 10,
          color: axisText.value, padding: 8, font: { size: 11 },
        },
      },
      y: {
        beginAtZero: true,
        border: { display: false },
        grid: { color: isDark.value ? '#ffffff20' : '#172d4619', drawTicks: false },
        ticks: {
          color: axisText.value, maxTicksLimit: 5, precision: 0, padding: 8,
          font: { size: 11 }, callback: value => compact.format(Number(value)),
        },
      },
    },
  }
})
</script>

<style scoped>
.dashboard-trend {
  position: relative;
  min-width: 0;
  min-height: 240px;
  flex: 1 0 240px;
}

.dashboard-trend :deep(canvas) {
  position: absolute;
  inset: 0;
}
</style>
