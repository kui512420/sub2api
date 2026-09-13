<template>
  <!--
    椒图账号的「用量」口径是积分，而不是 token / 美元窗口。
    数据直接取自 admin 下发的 credentials（points / jiaotu_status / phone），
    不做网络探测：刷新积分走号池维护接口（免费的 userBilling/page），
    避免在列表里为每个账号发一次上游请求。
  -->
  <div class="space-y-1" data-testid="jiaotu-points-cell">
    <div class="flex flex-wrap items-center gap-1.5">
      <span
        :class="['text-[10px] font-medium leading-4', platformTextClass('jiaotu')]"
        :title="pointsTooltip"
        data-testid="jiaotu-points-value"
      >
        {{ t('admin.accounts.jiaotu.points') }}: {{ pointsText }}
      </span>
      <span
        v-if="statusBadge"
        :class="[
          'inline-flex items-center rounded px-1 py-0.5 text-[10px] font-medium leading-4',
          statusBadge.class
        ]"
        data-testid="jiaotu-points-status"
      >
        {{ statusBadge.label }}
      </span>
    </div>
    <div v-if="identity" class="truncate text-[10px] text-gray-500 dark:text-gray-400" :title="identity">
      {{ identity }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account } from '@/types'
import { platformTextClass } from '@/utils/platformColors'
import { readJiaotuAccountView } from '@/components/account/credentialsBuilder'

interface Props {
  account: Account
}

const props = defineProps<Props>()
const { t } = useI18n()

const view = computed(() =>
  readJiaotuAccountView(props.account.credentials, props.account.credentials_status)
)

const pointsText = computed(() =>
  view.value.points == null ? t('admin.accounts.jiaotu.noPointsSnapshot') : String(view.value.points)
)

const identity = computed(() => {
  const parts: string[] = []
  if (view.value.poolId) parts.push(view.value.poolId)
  if (view.value.phone) parts.push(view.value.phone)
  return parts.join(' · ')
})

const pointsTooltip = computed(() =>
  t('admin.accounts.jiaotu.pointsHint')
)

const statusBadge = computed(() => {
  if (view.value.status === 'expired') {
    return {
      label: t('admin.accounts.jiaotu.statusExpired'),
      class: 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
    }
  }
  if (view.value.status === 'error') {
    return {
      label: t('admin.accounts.jiaotu.statusError'),
      class: 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
    }
  }
  if (view.value.status === 'ok') {
    return {
      label: t('admin.accounts.jiaotu.statusOk'),
      class: 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
    }
  }
  return null
})
</script>
