<template>
  <BaseDialog
    :show="show"
    :title="t('admin.accounts.jiaotu.poolImport.title')"
    width="normal"
    close-on-click-outside
    @close="handleClose"
  >
    <form id="jiaotu-pool-import-form" class="space-y-4" @submit.prevent="handleImport">
      <div class="text-sm text-gray-600 dark:text-dark-300">
        {{ t('admin.accounts.jiaotu.poolImport.hint') }}
      </div>
      <div
        class="rounded-lg border border-amber-200 bg-amber-50 p-3 text-xs text-amber-700 dark:border-amber-800 dark:bg-amber-900/20 dark:text-amber-400"
      >
        {{ t('admin.accounts.jiaotu.poolImport.warning') }}
      </div>

      <div>
        <label class="input-label">{{ t('admin.accounts.jiaotu.poolImport.contentLabel') }}</label>
        <textarea
          v-model="content"
          rows="8"
          class="input font-mono text-xs"
          data-testid="jiaotu-pool-content"
          :placeholder="t('admin.accounts.jiaotu.poolImport.contentPlaceholder')"
        ></textarea>
      </div>

      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div>
          <label class="input-label">{{ t('admin.accounts.jiaotu.poolImport.namePrefixLabel') }}</label>
          <input
            v-model="namePrefix"
            type="text"
            class="input"
            data-testid="jiaotu-pool-name-prefix"
            :placeholder="t('admin.accounts.jiaotu.poolImport.namePrefixPlaceholder')"
          />
        </div>
        <div>
          <label class="input-label">{{ t('admin.accounts.jiaotu.poolImport.groupLabel') }}</label>
          <select v-model.number="groupId" class="input" data-testid="jiaotu-pool-group">
            <option :value="0">{{ t('admin.accounts.jiaotu.poolImport.noGroupSelected') }}</option>
            <option v-for="group in jiaotuGroups" :key="group.id" :value="group.id">
              {{ group.name }}
            </option>
          </select>
          <p class="input-hint">{{ t('admin.accounts.jiaotu.poolImport.groupHint') }}</p>
        </div>
        <div>
          <label class="input-label">{{ t('admin.accounts.jiaotu.poolImport.concurrencyLabel') }}</label>
          <input v-model.number="concurrency" type="number" min="1" max="64" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.accounts.jiaotu.poolImport.priorityLabel') }}</label>
          <input v-model.number="priority" type="number" min="0" max="1000" class="input" />
        </div>
      </div>

      <label class="flex cursor-pointer items-start gap-2">
        <input v-model="updateExisting" type="checkbox" class="mt-0.5 text-primary-600" />
        <span class="text-sm text-gray-700 dark:text-gray-300">
          {{ t('admin.accounts.jiaotu.poolImport.updateExisting') }}
        </span>
      </label>

      <div
        v-if="result"
        class="space-y-2 rounded-xl border border-gray-200 p-4 dark:border-dark-700"
      >
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{
            t('admin.accounts.jiaotu.poolImport.resultSummary', {
              total: result.total,
              ready: result.ready,
              unusable: result.unusable
            })
          }}
        </div>
        <div v-if="result.target_size" class="text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.accounts.jiaotu.poolImport.targetSize', { count: result.target_size }) }}
        </div>
        <div
          v-if="result.errors?.length"
          class="max-h-40 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800"
        >
          <div
            v-for="(item, idx) in result.errors"
            :key="`err-${idx}`"
            class="whitespace-pre-wrap text-red-600 dark:text-red-400"
          >
            #{{ item.index }} {{ item.name }} — {{ item.message }}
          </div>
        </div>
        <div
          v-if="result.warnings?.length"
          class="max-h-40 overflow-auto rounded-lg bg-gray-50 p-3 font-mono text-xs dark:bg-dark-800"
        >
          <div
            v-for="(item, idx) in result.warnings"
            :key="`warn-${idx}`"
            class="whitespace-pre-wrap text-amber-600 dark:text-amber-400"
          >
            #{{ item.index }} {{ item.name }} — {{ item.message }}
          </div>
        </div>
      </div>
    </form>

    <!-- 号池维护：只打免费的 userBilling/page（刷新积分）与可选的每日签到，不消耗积分 -->
    <div class="mt-5 space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600">
      <div>
        <div class="text-sm font-medium text-gray-900 dark:text-white">
          {{ t('admin.accounts.jiaotu.maintenance.title') }}
        </div>
        <p class="mt-1 text-xs text-gray-500 dark:text-dark-400">
          {{ t('admin.accounts.jiaotu.maintenance.hint') }}
        </p>
      </div>
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div>
          <label class="input-label">{{ t('admin.accounts.jiaotu.maintenance.concurrencyLabel') }}</label>
          <input v-model.number="maintenanceConcurrency" type="number" min="1" max="10" class="input" />
        </div>
        <div>
          <label class="input-label">{{ t('admin.accounts.jiaotu.maintenance.limitLabel') }}</label>
          <input v-model.number="maintenanceLimit" type="number" min="0" class="input" />
        </div>
      </div>
      <div class="flex flex-wrap items-center gap-4">
        <label class="flex cursor-pointer items-center gap-2">
          <input v-model="maintenanceRefresh" type="checkbox" class="text-primary-600" />
          <span class="text-sm text-gray-700 dark:text-gray-300">
            {{ t('admin.accounts.jiaotu.maintenance.refreshPoints') }}
          </span>
        </label>
        <label class="flex cursor-pointer items-center gap-2">
          <input v-model="maintenanceSignIn" type="checkbox" class="text-primary-600" />
          <span class="text-sm text-gray-700 dark:text-gray-300">
            {{ t('admin.accounts.jiaotu.maintenance.signIn') }}
          </span>
        </label>
        <button
          type="button"
          class="btn btn-secondary ml-auto"
          :disabled="maintaining"
          data-testid="jiaotu-pool-maintenance-run"
          @click="handleMaintenance"
        >
          {{ maintaining ? t('admin.accounts.jiaotu.maintenance.running') : t('admin.accounts.jiaotu.maintenance.run') }}
        </button>
      </div>
      <div v-if="maintenanceResult" class="text-xs text-gray-600 dark:text-dark-300">
        {{
          t('admin.accounts.jiaotu.maintenance.success', {
            refreshed: maintenanceResult.refreshed,
            unchanged: maintenanceResult.unchanged,
            failed: maintenanceResult.failed,
            expired: maintenanceResult.expired
          })
        }}
        <span v-if="maintenanceResult.sign_in">
          ·
          {{
            t('admin.accounts.jiaotu.maintenance.signInSummary', {
              success: maintenanceResult.sign_in.success,
              already: maintenanceResult.sign_in.already,
              failed: maintenanceResult.sign_in.failed
            })
          }}
        </span>
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-3">
        <button class="btn btn-secondary" type="button" :disabled="importing" @click="handleClose">
          {{ t('common.close') }}
        </button>
        <button
          class="btn btn-primary"
          type="submit"
          form="jiaotu-pool-import-form"
          :disabled="importing"
          data-testid="jiaotu-pool-import-submit"
        >
          {{ importing ? t('admin.accounts.jiaotu.poolImport.importing') : t('admin.accounts.jiaotu.poolImport.submit') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useAppStore } from '@/stores/app'
import { adminAPI } from '@/api/admin'
import type { AdminGroup } from '@/types'
import type {
  JiaotuPoolImportPayload,
  JiaotuPoolImportResult,
  JiaotuPoolMaintenanceResult
} from '@/api/admin/accounts'

interface Props {
  show: boolean
  groups: AdminGroup[]
}

interface Emits {
  (e: 'close'): void
  (e: 'imported'): void
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const appStore = useAppStore()

const content = ref('')
const namePrefix = ref('')
const concurrency = ref(1)
const priority = ref(50)
const groupId = ref(0)
const updateExisting = ref(false)
const importing = ref(false)
const result = ref<JiaotuPoolImportResult | null>(null)

const maintenanceRefresh = ref(true)
const maintenanceSignIn = ref(false)
const maintenanceConcurrency = ref(5)
const maintenanceLimit = ref(0)
const maintaining = ref(false)
const maintenanceResult = ref<JiaotuPoolMaintenanceResult | null>(null)

/**
 * 只列 jiaotu 分组：号池账号必须挂在 platform=jiaotu 且开了图片/视频能力的分组，
 * 挂错分组不会被图片/视频链路选中。留空（不绑定）仍然允许。
 */
const jiaotuGroups = computed(() => props.groups.filter((group) => group.platform === 'jiaotu'))

watch(
  () => props.show,
  (open) => {
    if (open) {
      result.value = null
      maintenanceResult.value = null
    }
  }
)

function errorText(error: unknown, fallback: string): string {
  const err = error as { response?: { data?: { message?: string; detail?: string } } }
  return err?.response?.data?.message || err?.response?.data?.detail || fallback
}

const handleClose = () => {
  emit('close')
}

const handleImport = async () => {
  if (importing.value) return
  if (!content.value.trim()) {
    appStore.showError(t('admin.accounts.jiaotu.poolImport.emptyContent'))
    return
  }
  importing.value = true
  try {
    const payload: JiaotuPoolImportPayload = {
      content: content.value,
      concurrency: concurrency.value || 1,
      priority: priority.value || 0,
      update_existing: updateExisting.value
    }
    const prefix = namePrefix.value.trim()
    if (prefix) payload.name_prefix = prefix
    if (groupId.value > 0) payload.group_ids = [groupId.value]
    const res = await adminAPI.accounts.importJiaotuPool(payload)
    result.value = res
    appStore.showSuccess(
      t('admin.accounts.jiaotu.poolImport.success', {
        created: res.created,
        updated: res.updated,
        skipped: res.skipped,
        failed: res.failed
      })
    )
    emit('imported')
  } catch (error: unknown) {
    appStore.showError(errorText(error, t('admin.accounts.jiaotu.poolImport.failed')))
  } finally {
    importing.value = false
  }
}

const handleMaintenance = async () => {
  if (maintaining.value) return
  maintaining.value = true
  try {
    const res = await adminAPI.accounts.jiaotuPoolMaintenance({
      refresh_points: maintenanceRefresh.value,
      sign_in: maintenanceSignIn.value,
      concurrency: maintenanceConcurrency.value || 5,
      limit: maintenanceLimit.value || 0
    })
    maintenanceResult.value = res
    appStore.showSuccess(
      t('admin.accounts.jiaotu.maintenance.success', {
        refreshed: res.refreshed,
        unchanged: res.unchanged,
        failed: res.failed,
        expired: res.expired
      })
    )
    emit('imported')
  } catch (error: unknown) {
    appStore.showError(errorText(error, t('admin.accounts.jiaotu.maintenance.failed')))
  } finally {
    maintaining.value = false
  }
}
</script>
