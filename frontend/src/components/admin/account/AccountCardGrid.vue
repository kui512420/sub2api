<template>
  <div class="min-h-full p-3 sm:p-4">
    <div v-if="loading" class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
      <div
        v-for="index in 8"
        :key="index"
        class="h-[28rem] animate-pulse rounded-2xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-900"
      >
        <div class="flex items-start justify-between">
          <div class="flex gap-3">
            <div class="h-10 w-10 rounded-xl bg-gray-200 dark:bg-dark-700" />
            <div class="space-y-2">
              <div class="h-4 w-32 rounded bg-gray-200 dark:bg-dark-700" />
              <div class="h-3 w-24 rounded bg-gray-200 dark:bg-dark-700" />
            </div>
          </div>
          <div class="h-5 w-10 rounded-full bg-gray-200 dark:bg-dark-700" />
        </div>
        <div class="mt-6 space-y-3">
          <div class="h-16 rounded-xl bg-gray-100 dark:bg-dark-800" />
          <div class="h-28 rounded-xl bg-gray-100 dark:bg-dark-800" />
          <div class="h-20 rounded-xl bg-gray-100 dark:bg-dark-800" />
          <div class="h-9 rounded-lg bg-gray-100 dark:bg-dark-800" />
        </div>
      </div>
    </div>

    <div v-else-if="accounts.length === 0" class="rounded-2xl border border-gray-200 bg-white p-12 text-center dark:border-dark-700 dark:bg-dark-900">
      <Icon name="inbox" size="xl" class="mx-auto mb-4 text-gray-400 dark:text-dark-500" />
      <p class="text-lg font-medium text-gray-900 dark:text-gray-100">{{ t('empty.noData') }}</p>
    </div>

    <div v-else class="grid grid-cols-1 items-stretch gap-4 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
      <article
        v-for="account in accounts"
        :key="account.id"
        class="group flex min-h-[28rem] flex-col rounded-2xl border bg-white p-4 shadow-sm transition-shadow hover:shadow-md dark:bg-dark-900"
        :class="[
          isSelected(account.id)
            ? 'border-primary-400 ring-2 ring-primary-500/20 dark:border-primary-500'
            : 'border-gray-200 dark:border-dark-700',
          account.status === 'error' ? 'border-red-200 dark:border-red-900/50' : ''
        ]"
      >
        <header class="flex items-start justify-between gap-3">
          <div class="flex min-w-0 items-start gap-3">
            <input
              type="checkbox"
              class="mt-1 h-4 w-4 flex-shrink-0 cursor-pointer rounded border-gray-300 text-primary-600 focus:ring-primary-500 dark:border-dark-600 dark:bg-dark-800"
              :checked="isSelected(account.id)"
              :aria-label="`${t('common.selectAll')}: ${account.name}`"
              @click.stop
              @change="$emit('toggle-select', account.id)"
            />
            <div class="flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl bg-primary-50 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
              <PlatformIcon platform="openai" size="lg" />
            </div>
            <div class="min-w-0">
              <div class="flex items-center gap-1.5">
                <HelpTooltip
                  v-if="homepageUrl(account)"
                  :content="homepageUrl(account)"
                  width-class="w-max max-w-sm break-all"
                >
                  <template #trigger>
                    <a
                      :href="homepageUrl(account)"
                      target="_blank"
                      rel="noopener noreferrer"
                      class="truncate border-b border-dotted border-gray-300 text-sm font-semibold text-gray-900 dark:border-dark-600 dark:text-white"
                    >{{ account.name }}</a>
                  </template>
                </HelpTooltip>
                <span v-else class="truncate text-sm font-semibold text-gray-900 dark:text-white">{{ account.name }}</span>
              </div>
              <p class="mt-0.5 truncate text-xs text-gray-500 dark:text-gray-400" :title="displayEmail(account)">
                {{ displayEmail(account) || `#${account.id}` }}
              </p>
            </div>
          </div>
          <button
            type="button"
            class="relative inline-flex h-5 w-9 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50 dark:focus:ring-offset-dark-900"
            :class="account.schedulable ? 'bg-primary-500' : 'bg-gray-200 dark:bg-dark-600'"
            :disabled="togglingId === account.id"
            :aria-pressed="account.schedulable"
            :aria-label="account.schedulable ? t('admin.accounts.schedulableEnabled') : t('admin.accounts.schedulableDisabled')"
            @click.stop="$emit('toggle-schedulable', account)"
          >
            <span class="pointer-events-none inline-block h-4 w-4 rounded-full bg-white shadow transition-transform" :class="account.schedulable ? 'translate-x-4' : 'translate-x-0'" />
          </button>
        </header>

        <div class="mt-3 flex flex-wrap items-center gap-1.5">
          <PlatformTypeBadge
            :platform="account.platform"
            :type="account.type"
            :auth-mode="openAIAuthMode(account)"
            :plan-type="accountPlanType(account)"
            :privacy-mode="privacyMode(account)"
            :subscription-expires-at="subscriptionExpiresAt(account)"
          />
          <span class="rounded-md border border-gray-200 px-2 py-0.5 text-[11px] text-gray-500 dark:border-dark-600 dark:text-gray-400">
            {{ t('admin.accounts.columns.priority') }} {{ account.priority }}
          </span>
          <span
            v-if="antigravityTierLabel(account)"
            class="rounded-md bg-purple-50 px-2 py-0.5 text-[11px] font-medium text-purple-700 dark:bg-purple-900/30 dark:text-purple-300"
          >{{ antigravityTierLabel(account) }}</span>
        </div>

        <div class="mt-3 flex items-center justify-between gap-2 rounded-xl border border-gray-100 bg-gray-50/70 px-3 py-2 dark:border-dark-700 dark:bg-dark-800/60">
          <AccountStatusIndicator :account="account" @show-temp-unsched="$emit('show-temp-unsched', $event)" />
          <span v-if="account.last_used_at" class="truncate text-[11px] text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.columns.lastUsed') }}: {{ formatRelativeTime(account.last_used_at) }}
          </span>
          <span v-else class="text-[11px] text-gray-400 dark:text-gray-500">{{ t('common.time.never') }}</span>
        </div>

        <div class="mt-3">
          <div class="mb-2 flex items-center justify-between">
            <span class="text-xs font-semibold uppercase tracking-wide text-gray-500 dark:text-gray-400">{{ t('admin.accounts.stats.todayOverview') }}</span>
            <span class="text-[11px] text-gray-400 dark:text-gray-500">{{ t('admin.accounts.stats.recentActivity') }}</span>
          </div>
          <div class="grid grid-cols-3 gap-2">
            <div v-for="metric in [
              { label: t('admin.accounts.stats.requests'), value: statsFor(account) ? formatNumber(statsFor(account)?.requests) : '-' },
              { label: t('admin.accounts.stats.tokens'), value: statsFor(account) ? formatTokens(statsFor(account)?.tokens) : '-' },
              { label: t('usage.accountBilled'), value: statsFor(account) ? formatCurrency(statsFor(account)?.cost) : '-' }
            ]" :key="metric.label" class="rounded-lg border border-gray-100 bg-gray-50/60 px-2 py-2 dark:border-dark-700 dark:bg-dark-800/60">
              <div class="truncate text-[10px] text-gray-500 dark:text-gray-400">{{ metric.label }}</div>
              <div class="mt-0.5 truncate text-sm font-semibold tabular-nums text-gray-800 dark:text-gray-100">{{ metric.value }}</div>
            </div>
          </div>
        </div>

        <div class="account-card-usage mt-3 rounded-xl border border-gray-100 bg-white p-3 dark:border-dark-700 dark:bg-dark-800/30">
          <div class="mb-3 flex items-center justify-between border-b border-gray-100 pb-2 dark:border-dark-700">
            <span class="text-xs font-semibold text-gray-700 dark:text-gray-200">{{ t('admin.accounts.columns.usageWindows') }}</span>
            <span class="text-[11px] font-medium text-gray-400 dark:text-gray-500">{{ t('admin.accounts.columns.capacity') }}</span>
          </div>
          <AccountUsageCell
            :account="account"
            :today-stats="statsFor(account)"
            :today-stats-loading="statsLoading"
            :manual-refresh-token="manualRefreshToken"
            :batched-usage="usageFor(account)"
            :batched-usage-error="usageErrors[String(account.id)] ?? null"
            :batched-usage-loading="usageLoading[String(account.id)] === true"
            :request-batched-usage="requestBatchedUsage"
            @account-updated="$emit('account-updated', $event)"
          />
          <div class="mt-2 border-t border-gray-100 pt-2 dark:border-dark-700">
            <div class="mb-2">
              <div class="mb-1.5 flex items-center justify-between text-[11px]">
                <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.columns.capacity') }}</span>
                <span class="font-mono font-medium text-gray-700 dark:text-gray-200">{{ currentConcurrency(account) }} / {{ account.concurrency }}</span>
              </div>
              <div class="h-1.5 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700" :aria-label="`${t('admin.accounts.columns.capacity')}: ${currentConcurrency(account)} / ${account.concurrency}`" role="progressbar" :aria-valuenow="currentConcurrency(account)" aria-valuemin="0" :aria-valuemax="Math.max(account.concurrency, 0)">
                <div class="h-full rounded-full transition-all duration-300" :class="capacityBarClass(account)" :style="{ width: `${capacityPercent(account)}%` }" />
              </div>
            </div>
            <AccountCapacityCell :account="account" :show-concurrency="false" />
          </div>
        </div>

        <div v-if="account.notes || account.proxy || account.expires_at" class="mt-3 space-y-1.5 text-xs text-gray-500 dark:text-gray-400">
          <div v-if="account.notes" class="flex items-start gap-1.5"><Icon name="document" size="xs" class="mt-0.5 flex-shrink-0" /><span class="truncate" :title="account.notes">{{ account.notes }}</span></div>
          <div v-if="account.proxy" class="flex items-center gap-1.5"><Icon name="globe" size="xs" class="flex-shrink-0" /><span class="truncate">{{ account.proxy.name }}<template v-if="account.proxy.country_code"> ({{ account.proxy.country_code }})</template></span></div>
          <div v-if="account.expires_at" class="flex items-center gap-1.5"><Icon name="clock" size="xs" class="flex-shrink-0" /><span>{{ formatExpiresAt(account.expires_at) }}</span></div>
        </div>

        <footer class="mt-auto flex items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-dark-700">
          <span class="text-[11px] text-gray-400 dark:text-gray-500">#{{ account.id }}</span>
          <div class="flex items-center gap-1">
            <button type="button" class="card-action text-red-500 hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20" :aria-label="t('common.delete')" @click="$emit('delete', account)"><Icon name="trash" size="sm" /></button>
            <button type="button" class="card-action text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" :aria-label="t('common.edit')" @click="$emit('edit', account)"><Icon name="edit" size="sm" /></button>
            <button type="button" class="card-action text-gray-500 hover:bg-gray-100 hover:text-primary-600 dark:hover:bg-dark-700" :aria-label="t('common.more')" @click="$emit('menu', account, $event)"><Icon name="more" size="sm" /></button>
          </div>
        </footer>
      </article>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account, AccountListItem, AccountUsageInfo, AdminGroup, WindowStats } from '@/types'
import Icon from '@/components/icons/Icon.vue'
import HelpTooltip from '@/components/common/HelpTooltip.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountUsageCell from '@/components/account/AccountUsageCell.vue'
import AccountCapacityCell from '@/components/account/AccountCapacityCell.vue'
import { formatCurrency, formatDateTime, formatNumber, formatRelativeTime } from '@/utils/format'
import { sanitizeUrl } from '@/utils/url'

const props = withDefaults(defineProps<{
  accounts: AccountListItem[]
  groups?: AdminGroup[]
  selectedIds?: number[]
  loading?: boolean
  statsByAccountId?: Record<string, WindowStats>
  statsLoading?: boolean
  statsError?: string | null
  manualRefreshToken?: number
  usageByAccountId?: Record<string, AccountUsageInfo | null>
  usageErrors?: Record<string, string | null>
  usageLoading?: Record<string, boolean>
  togglingId?: number | null
  requestBatchedUsage?: ((account: Account, options?: { force?: boolean }) => void) | null
}>(), {
  groups: () => [],
  selectedIds: () => [],
  loading: false,
  statsByAccountId: () => ({}),
  statsLoading: false,
  statsError: null,
  manualRefreshToken: 0,
  usageByAccountId: () => ({}),
  usageErrors: () => ({}),
  usageLoading: () => ({}),
  togglingId: null,
  requestBatchedUsage: null
})

defineEmits<{
  (event: 'toggle-select', id: number): void
  (event: 'toggle-schedulable', account: AccountListItem): void
  (event: 'show-temp-unsched', account: Account): void
  (event: 'account-updated', account: Account): void
  (event: 'delete', account: AccountListItem): void
  (event: 'edit', account: AccountListItem): void
  (event: 'menu', account: AccountListItem, mouseEvent: MouseEvent): void
}>()

const { t } = useI18n()
const selectedSet = computed(() => new Set(props.selectedIds))
const isSelected = (id: number) => selectedSet.value.has(id)
const statsFor = (account: AccountListItem) => props.statsByAccountId[String(account.id)] ?? null
const usageFor = (account: AccountListItem) => props.usageByAccountId[String(account.id)] ?? null
const displayEmail = (account: AccountListItem) => {
  const row = account as any
  return row.extra?.email_address || row.extra?.email || row.credentials?.email || row.parent_email || ''
}
const homepageUrl = (account: AccountListItem) => {
  if (account.type !== 'apikey' || typeof account.credentials?.base_url !== 'string') return ''
  const baseUrl = sanitizeUrl(account.credentials.base_url)
  return baseUrl ? new URL(baseUrl).origin : ''
}
const openAIAuthMode = (account: AccountListItem) => account.platform === 'openai' && account.type === 'oauth' && typeof account.credentials?.auth_mode === 'string' ? account.credentials.auth_mode : undefined
const accountPlanType = (account: AccountListItem) => (account as any).credentials?.plan_type || account.parent_plan_type
const privacyMode = (account: AccountListItem): string | undefined => {
  const row = account as any
  return typeof row.extra?.privacy_mode === 'string' ? row.extra.privacy_mode : account.parent_privacy_mode
}
const subscriptionExpiresAt = (account: AccountListItem): string | undefined => {
  const row = account as any
  return typeof row.credentials?.subscription_expires_at === 'string' ? row.credentials.subscription_expires_at : account.parent_subscription_expires_at
}
const antigravityTierLabel = (account: AccountListItem) => {
  if (account.platform !== 'antigravity') return ''
  const loadCodeAssist = account.extra?.load_code_assist as Record<string, any> | undefined
  const tier = loadCodeAssist?.paidTier?.id || loadCodeAssist?.currentTier?.id
  if (tier === 'free-tier') return t('admin.accounts.tier.free')
  if (tier === 'g1-pro-tier') return t('admin.accounts.tier.pro')
  if (tier === 'g1-ultra-tier') return t('admin.accounts.tier.ultra')
  return ''
}
const formatTokens = (value: number | undefined) => {
  if (value == null) return '-'
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return String(value)
}
const currentConcurrency = (account: AccountListItem) => Math.max(Number(account.current_concurrency ?? 0), 0)
const capacityPercent = (account: AccountListItem) => {
  const max = Number(account.concurrency ?? 0)
  if (!Number.isFinite(max) || max <= 0) return 0
  return Math.min((currentConcurrency(account) / max) * 100, 100)
}
const capacityBarClass = (account: AccountListItem) => {
  const percent = capacityPercent(account)
  if (percent >= 100) return 'bg-red-500'
  if (percent >= 75) return 'bg-amber-500'
  return 'bg-primary-500'
}
const formatExpiresAt = (value: number | null) => value ? formatDateTime(new Date(value * 1000), { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false }, 'sv-SE') : '-'

</script>

<style scoped>
.card-action {
  @apply inline-flex h-8 w-8 items-center justify-center rounded-lg transition-colors focus:outline-none focus:ring-2 focus:ring-primary-500 focus:ring-offset-1 dark:focus:ring-offset-dark-900;
}

.account-card-usage :deep(.usage-progress-row) {
  display: grid;
  grid-template-columns: 2.25rem minmax(2.5rem, 1fr) 3rem auto;
  align-items: center;
  column-gap: 0.5rem;
}

.account-card-usage :deep(.usage-progress-track) {
  width: 100%;
  height: 0.5rem;
}

.account-card-usage :deep(.usage-progress-percent) {
  width: auto;
  text-align: left;
}

.account-card-usage :deep(.usage-window-stat-list) {
  flex-wrap: wrap;
  gap: 0.35rem;
}

.account-card-usage :deep(.usage-window-stats) {
  margin-bottom: 0.4rem;
}
</style>
