<template>
  <div class="space-y-6">
    <!-- Header / filters -->
    <div class="card p-6">
      <div class="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">
            {{ t('admin.media.title') }}
          </h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            {{ t('admin.media.description') }}
            <span v-if="bucket" class="ml-1 inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:bg-dark-700 dark:text-gray-300">
              {{ t('admin.media.bucket') }}: {{ bucket }}
            </span>
          </p>
        </div>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loading" @click="reload">
          {{ t('admin.media.refresh') }}
        </button>
      </div>

      <div class="mt-4 flex flex-wrap items-center gap-2">
        <button
          v-for="opt in kindOptions"
          :key="opt.value"
          type="button"
          class="rounded-full px-3 py-1 text-xs font-medium transition-colors"
          :class="kind === opt.value
            ? 'bg-primary-600 text-white'
            : 'bg-gray-100 text-gray-600 hover:bg-gray-200 dark:bg-dark-700 dark:text-gray-300 dark:hover:bg-dark-600'"
          @click="switchKind(opt.value)"
        >
          {{ opt.label }}
        </button>
        <span v-if="!loading && objects.length" class="ml-auto text-xs text-gray-400">
          {{ t('admin.media.total', { count: objects.length }) }}
        </span>
      </div>
    </div>

    <!-- Object storage not configured -->
    <div v-if="unavailable" class="card p-8 text-center">
      <h4 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.media.unavailable.title') }}</h4>
      <p class="mx-auto mt-2 max-w-xl text-sm text-gray-500 dark:text-gray-400">{{ t('admin.media.unavailable.description') }}</p>
      <router-link to="/admin/settings" class="btn btn-primary btn-sm mt-4 inline-flex">
        {{ t('admin.media.unavailable.goConfigure') }}
      </router-link>
    </div>

    <!-- Error -->
    <div v-else-if="error" class="card p-6 text-center">
      <p class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
      <button type="button" class="btn btn-secondary btn-sm mt-3" @click="reload">{{ t('admin.media.refresh') }}</button>
    </div>

    <!-- Initial loading -->
    <div v-else-if="loading && !objects.length" class="card p-10 text-center text-sm text-gray-400">
      {{ t('admin.media.loading') }}
    </div>

    <!-- Empty -->
    <div v-else-if="!objects.length" class="card p-10 text-center text-sm text-gray-400">
      {{ t('admin.media.empty') }}
    </div>

    <!-- Grid -->
    <template v-else>
      <div class="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-4 xl:grid-cols-5">
        <div
          v-for="obj in objects"
          :key="obj.key"
          class="card group overflow-hidden p-0"
        >
          <button
            type="button"
            class="relative flex h-40 w-full items-center justify-center overflow-hidden bg-gray-900"
            @click="preview = obj"
          >
            <img
              v-if="obj.kind === 'image'"
              :src="obj.url"
              :alt="shortKey(obj.key)"
              loading="lazy"
              class="h-full w-full object-contain"
            />
            <video
              v-else-if="obj.kind === 'video'"
              :src="obj.url"
              preload="metadata"
              muted
              class="h-full w-full object-contain"
            ></video>
            <div v-else class="px-3 text-center text-xs text-gray-300">{{ shortKey(obj.key) }}</div>
            <span
              class="absolute left-2 top-2 rounded-full bg-black/60 px-2 py-0.5 text-[10px] font-medium text-white"
            >{{ kindLabel(obj.kind) }}</span>
          </button>

          <div class="space-y-1 p-3">
            <p class="truncate text-xs font-medium text-gray-800 dark:text-gray-200" :title="obj.key">{{ shortKey(obj.key) }}</p>
            <p class="text-[11px] text-gray-400">{{ formatSize(obj.size_bytes) }} · {{ formatDate(obj.last_modified) }}</p>
            <div class="flex items-center gap-2 pt-1">
              <a
                :href="obj.url"
                target="_blank"
                rel="noopener"
                class="text-[11px] text-primary-600 hover:underline dark:text-primary-400"
              >{{ t('admin.media.actions.open') }}</a>
              <button
                type="button"
                class="ml-auto text-[11px] text-red-600 hover:underline disabled:opacity-50 dark:text-red-400"
                :disabled="deletingKey === obj.key"
                @click="remove(obj)"
              >{{ deletingKey === obj.key ? t('common.loading') : t('admin.media.actions.delete') }}</button>
            </div>
          </div>
        </div>
      </div>

      <div v-if="isTruncated" class="text-center">
        <button type="button" class="btn btn-secondary btn-sm" :disabled="loadingMore" @click="loadMore">
          {{ loadingMore ? t('admin.media.loadingMore') : t('admin.media.loadMore') }}
        </button>
      </div>
    </template>

    <!-- Lightbox preview -->
    <teleport to="body">
      <div
        v-if="preview"
        class="fixed inset-0 z-50 flex items-center justify-center bg-black/80 p-6"
        @click.self="preview = null"
      >
        <div class="flex max-h-full w-full max-w-4xl flex-col">
          <div class="mb-3 flex items-center justify-between gap-3 text-white">
            <span class="truncate text-sm">{{ shortKey(preview.key) }}</span>
            <div class="flex flex-shrink-0 items-center gap-3">
              <a :href="preview.url" target="_blank" rel="noopener" class="text-sm underline">{{ t('admin.media.actions.download') }}</a>
              <button type="button" class="text-sm" @click="preview = null">✕</button>
            </div>
          </div>
          <img v-if="preview.kind === 'image'" :src="preview.url" :alt="preview.key" class="max-h-[80vh] w-full object-contain" />
          <video v-else-if="preview.kind === 'video'" :src="preview.url" controls autoplay class="max-h-[80vh] w-full"></video>
        </div>
      </div>
    </teleport>

    <TotpStepUpDialog :controller="stepUp" />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api'
import { useAppStore } from '@/stores'
import { useStepUp, isStepUpBlocked, isStepUpCancelled, stepUpBlockReason } from '@/composables/useStepUp'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import type { StoredMediaObject, MediaKindFilter } from '@/api/admin/media'

const { t } = useI18n()
const appStore = useAppStore()
const stepUp = useStepUp()

const kind = ref<MediaKindFilter>('all')
const objects = ref<StoredMediaObject[]>([])
const bucket = ref('')
const nextToken = ref('')
const isTruncated = ref(false)
const loading = ref(false)
const loadingMore = ref(false)
const unavailable = ref(false)
const error = ref('')
const deletingKey = ref('')
const preview = ref<StoredMediaObject | null>(null)

const kindOptions = computed(() => [
  { value: 'all' as MediaKindFilter, label: t('admin.media.tabs.all') },
  { value: 'image' as MediaKindFilter, label: t('admin.media.tabs.image') },
  { value: 'video' as MediaKindFilter, label: t('admin.media.tabs.video') }
])

function isUnavailableError(err: unknown): boolean {
  const e = (err ?? {}) as { status?: number; code?: string | number; reason?: string }
  if (e.status === 503) return true
  const marker = String(e.code ?? e.reason ?? '')
  return marker.includes('MANAGED_STORAGE')
}

async function fetchPage(reset: boolean, token: string) {
  if (reset) {
    loading.value = true
    error.value = ''
    unavailable.value = false
  } else {
    loadingMore.value = true
  }
  try {
    const page = await adminAPI.media.listMediaObjects({
      kind: kind.value,
      continuation_token: token || undefined,
      limit: 48
    })
    bucket.value = page.bucket || ''
    isTruncated.value = !!page.is_truncated
    nextToken.value = page.next_token || ''
    objects.value = reset ? page.objects : [...objects.value, ...page.objects]
  } catch (err) {
    if (reset && isUnavailableError(err)) {
      unavailable.value = true
    } else {
      error.value = (err as { message?: string })?.message || t('admin.media.loadFailed')
    }
  } finally {
    loading.value = false
    loadingMore.value = false
  }
}

function reload() {
  objects.value = []
  void fetchPage(true, '')
}

function switchKind(value: MediaKindFilter) {
  if (kind.value === value) return
  kind.value = value
  reload()
}

function loadMore() {
  if (!nextToken.value || loadingMore.value) return
  void fetchPage(false, nextToken.value)
}

async function remove(obj: StoredMediaObject) {
  if (!window.confirm(t('admin.media.deleteConfirm'))) return
  deletingKey.value = obj.key
  try {
    await stepUp.run(() => adminAPI.media.deleteMediaObject(obj.key))
    objects.value = objects.value.filter((item) => item.key !== obj.key)
    if (preview.value?.key === obj.key) preview.value = null
    appStore.showSuccess(t('admin.media.deleted'))
  } catch (err) {
    if (isStepUpCancelled(err)) return
    if (isStepUpBlocked(err)) {
      appStore.showError(
        stepUpBlockReason(err) === 'STEP_UP_ADMIN_API_KEY_FORBIDDEN'
          ? t('stepUp.adminApiKeyForbidden')
          : t('stepUp.notEnabled'),
      )
      return
    }
    appStore.showError((err as { message?: string })?.message || t('admin.media.deleteFailed'))
  } finally {
    deletingKey.value = ''
  }
}

function kindLabel(kindValue: string): string {
  if (kindValue === 'image') return t('admin.media.kind.image')
  if (kindValue === 'video') return t('admin.media.kind.video')
  return t('admin.media.kind.other')
}

function shortKey(key: string): string {
  const idx = key.lastIndexOf('/')
  return idx >= 0 ? key.slice(idx + 1) : key
}

function formatSize(bytes: number): string {
  if (!bytes || bytes <= 0) return '-'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
  return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
}

function formatDate(value?: string): string {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

onMounted(() => {
  void fetchPage(true, '')
})
</script>
