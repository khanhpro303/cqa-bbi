<template>
  <div>
    <div class="text-caption text-grey mt-2 mb-1">Gửi cảnh báo thêm qua (tùy chọn):</div>
    <v-card v-for="(o, idx) in model" :key="idx" variant="outlined" class="pa-3 mb-2">
      <div class="d-flex align-center mb-2">
        <v-chip size="x-small" :color="o.type === 'telegram' ? 'blue' : 'orange'" variant="tonal">
          {{ o.type === 'telegram' ? 'Telegram' : 'Email' }}
        </v-chip>
        <v-spacer />
        <v-btn icon="mdi-close" size="x-small" variant="text" @click="remove(idx)" />
      </div>

      <v-select
        v-model="o.type"
        :items="typeItems"
        label="Loại"
        density="compact"
        class="mb-2"
        hide-details
      />

      <template v-if="o.type === 'telegram'">
        <v-text-field v-model="o.bot_token" label="Bot Token" density="compact" class="mb-2" hide-details />
        <v-text-field v-model="o.chat_id" label="Group ID" density="compact" hide-details
          hint="Số âm, ví dụ: -1001234567890" persistent-hint />
      </template>

      <template v-else>
        <v-row dense>
          <v-col cols="8"><v-text-field v-model="o.smtp_host" label="SMTP Host" density="compact" hide-details /></v-col>
          <v-col cols="4"><v-text-field v-model.number="o.smtp_port" label="Port" type="number" density="compact" hide-details /></v-col>
        </v-row>
        <v-row dense class="mt-1">
          <v-col cols="6"><v-text-field v-model="o.smtp_user" label="Username" density="compact" hide-details /></v-col>
          <v-col cols="6"><v-text-field v-model="o.smtp_pass" label="Password" type="password" density="compact" hide-details /></v-col>
        </v-row>
        <v-text-field v-model="o.from" label="Email gửi" density="compact" class="mb-2 mt-2" hide-details />
        <v-text-field v-model="o.to" label="Email nhận" density="compact" hide-details
          hint="Nhiều email cách nhau bằng dấu phẩy" persistent-hint />
      </template>
    </v-card>

    <v-btn variant="text" size="small" color="primary" @click="add">
      <v-icon start>mdi-plus</v-icon>
      Thêm kênh cảnh báo
    </v-btn>
  </div>
</template>

<script setup lang="ts">
export interface AlertOutput {
  type: string
  bot_token?: string
  chat_id?: string
  smtp_host?: string
  smtp_port?: number
  smtp_user?: string
  smtp_pass?: string
  from?: string
  to?: string
}

const model = defineModel<AlertOutput[]>({ default: () => [] })

const typeItems = [
  { title: 'Telegram', value: 'telegram' },
  { title: 'Email', value: 'email' },
]

function add() {
  model.value = [...model.value, { type: 'telegram' }]
}

function remove(idx: number) {
  model.value = model.value.filter((_, i) => i !== idx)
}
</script>
