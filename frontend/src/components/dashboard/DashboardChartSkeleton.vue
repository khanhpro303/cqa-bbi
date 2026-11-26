<template>
  <div
    class="dash-chart-skel"
    :class="`dash-chart-skel--${variant}`"
    role="status"
    :aria-label="label || $t('loading')"
  >
    <template v-if="variant === 'ranking'">
      <div v-for="(row, idx) in rankingRows" :key="idx" class="dash-chart-skel__rank-row">
        <div class="dash-chart-skel__rank-caption">
          <span class="dash-skel dash-skel--pill" :style="{ width: `${row.nameWidth}%`, height: '13px' }" />
          <span class="dash-skel dash-skel--pill" style="width: 64px; height: 13px;" />
        </div>
        <div class="dash-chart-skel__rank-track">
          <span class="dash-skel dash-skel--pill" :style="{ width: `${row.barWidth}%`, height: '7px' }" />
        </div>
      </div>
    </template>
    <template v-else>
      <div v-if="legendCount > 0" class="dash-chart-skel__legend" aria-hidden="true">
        <span v-for="idx in legendCount" :key="idx" class="dash-chart-skel__legend-item">
          <span class="dash-skel dash-skel--pill" style="width: 18px; height: 3px;" />
          <span
            class="dash-skel dash-skel--pill"
            :style="{ width: `${legendWidths[(idx - 1) % legendWidths.length]}px`, height: '11px' }"
          />
        </span>
      </div>
      <div class="dash-chart-skel__plot" aria-hidden="true">
        <div class="dash-chart-skel__grid">
          <span v-for="n in 4" :key="n" class="dash-chart-skel__grid-line" />
        </div>
        <div class="dash-chart-skel__bars">
          <span
            v-for="(pct, idx) in barHeights"
            :key="idx"
            class="dash-skel dash-chart-skel__bar"
            :style="{ height: `${pct}%` }"
          />
        </div>
      </div>
      <div class="dash-chart-skel__ticks" aria-hidden="true">
        <span v-for="n in 6" :key="n" class="dash-skel dash-skel--pill" style="width: 34px; height: 10px;" />
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: 'line' | 'ranking'
    legendCount?: number
    label?: string
  }>(),
  {
    variant: 'line',
    legendCount: 0,
    label: '',
  },
)

const legendWidths = [68, 84, 72]
const barHeights = [26, 40, 34, 56, 48, 70, 62, 52, 76, 64, 82, 68]
const rankingRows = [
  { nameWidth: 44, barWidth: 100 },
  { nameWidth: 36, barWidth: 74 },
  { nameWidth: 50, barWidth: 52 },
  { nameWidth: 32, barWidth: 34 },
  { nameWidth: 40, barWidth: 20 },
]
</script>

<style scoped>
.dash-chart-skel {
  display: flex;
  flex: 1 0 240px;
  flex-direction: column;
  min-height: 240px;
  min-width: 0;
  padding-top: 4px;
}

.dash-chart-skel--ranking {
  padding: 3px 6px 3px 0;
}

.dash-chart-skel__legend {
  display: flex;
  flex-wrap: wrap;
  gap: 14px;
  margin-bottom: 12px;
}

.dash-chart-skel__legend-item {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.dash-chart-skel__plot {
  position: relative;
  flex: 1;
  min-height: 170px;
  display: flex;
  align-items: flex-end;
  padding: 8px 6px 0;
}

.dash-chart-skel__grid {
  position: absolute;
  inset: 8px 0 0;
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  pointer-events: none;
}

.dash-chart-skel__grid-line {
  display: block;
  width: 100%;
  height: 1px;
  background: rgba(var(--v-theme-on-surface), 0.065);
}

.dash-chart-skel__bars {
  position: relative;
  z-index: 1;
  display: grid;
  grid-template-columns: repeat(12, minmax(0, 1fr));
  align-items: end;
  gap: clamp(4px, 1.2vw, 10px);
  width: 100%;
  height: 100%;
}

.dash-chart-skel__bar {
  width: 100%;
  border-radius: 6px 6px 2px 2px;
  opacity: 0.72;
}

.dash-chart-skel__ticks {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 6px 2px;
}

.dash-chart-skel__rank-row + .dash-chart-skel__rank-row {
  margin-top: 14px;
}

.dash-chart-skel__rank-caption {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 6px;
}

.dash-chart-skel__rank-track {
  height: 7px;
  border-radius: 6px;
  overflow: hidden;
  background: rgba(var(--v-theme-on-surface), 0.05);
}
</style>
