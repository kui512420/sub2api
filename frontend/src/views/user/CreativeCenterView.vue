<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { keysAPI } from '@/api'
import { generateCreativeImage, submitCreativeImageTask, getCreativeImageTask, createCreativeVideo, type CreativeImageTask } from '@/api/creative'
import type { ApiKey } from '@/types'
import CreativeSplitWorkspace, { type CreativeWorkspaceParameter } from '@/components/creative/CreativeSplitWorkspace.vue'

type CreativeSection = 'image' | 'video' | 'history'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const activeSection = computed<CreativeSection>(() => {
  const section = route.path.split('/')[2]
  return section === 'video' || section === 'history' ? section : 'image'
})

const imageForm = ref({ apiKeyId: '', model: 'gpt-image-1', prompt: '', size: '1024x1024', quality: 'auto', n: 1, response_format: 'url' })
const videoForm = ref({ apiKeyId: '', model: 'sora-1', prompt: '', duration: 5, size: '1280x720', input_references: [] as string[] })
const videoReferencePreviews = ref<string[]>([])
const apiKeys = ref<ApiKey[]>([])
const imageSubmitting = ref(false)
const videoSubmitting = ref(false)
const imageError = ref('')
const videoError = ref('')
const imageTask = ref<CreativeImageTask | null>(null)
const videoTask = ref<{ id?: string, status?: string } | null>(null)

const imageCostEstimate = computed(() => `$${(Number(imageForm.value.n) * 0.04).toFixed(2)} 起`)
const videoCostEstimate = computed(() => `$${(Number(videoForm.value.duration) * 0.18).toFixed(2)} 起`)

function handleVideoReferenceUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  if (!files.length) return
  const invalid = files.find(file => !file.type.startsWith('image/'))
  if (invalid) {
    videoError.value = t('creativeCenter.workspace.form.referenceImageOnly')
    input.value = ''
    return
  }
  Promise.all(files.map(file => new Promise<string>((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result || ''))
    reader.onerror = () => reject(reader.error)
    reader.readAsDataURL(file)
  }))).then((dataUrls) => {
    videoForm.value.input_references.push(...dataUrls)
    videoReferencePreviews.value.push(...dataUrls)
    input.value = ''
  }).catch(() => {
    videoError.value = t('creativeCenter.workspace.form.submitFailed')
  })
}

function removeVideoReference(index: number) {
  videoForm.value.input_references.splice(index, 1)
  videoReferencePreviews.value.splice(index, 1)
}

function clearVideoReferences() {
  videoForm.value.input_references = []
  videoReferencePreviews.value = []
}

async function generateImage() {
  imageError.value = ''
  if (!imageForm.value.prompt.trim()) {
    imageError.value = t('creativeCenter.workspace.form.promptRequired')
    return
  }
  const apiKey = apiKeys.value.find(key => String(key.id) === imageForm.value.apiKeyId)
  if (!apiKey?.key) {
    imageError.value = t('creativeCenter.workspace.form.keyRequired')
    return
  }
  imageSubmitting.value = true
  imageTask.value = null
  try {
    const payload = {
      model: imageForm.value.model,
      prompt: imageForm.value.prompt.trim(),
      size: imageForm.value.size,
      quality: imageForm.value.quality,
      n: Number(imageForm.value.n),
      response_format: imageForm.value.response_format,
    }
    try {
      imageTask.value = await submitCreativeImageTask(apiKey.key, payload)
    } catch (error: any) {
      // Async tasks require R2 to be enabled. Keep the workspace usable before
      // storage is configured by falling back to the synchronous OpenAI response.
      if (Number(error?.status) !== 404) throw error
      const response = await generateCreativeImage(apiKey.key, payload)
      imageTask.value = { id: `sync_${Date.now()}`, status: 'completed', result: response }
    }
    // Poll the async task so the eventual R2 URL is shown without exposing provider storage.
    const taskId = imageTask.value?.id || imageTask.value?.task_id
    if (taskId) {
      for (let attempt = 0; attempt < 30 && !['completed', 'failed', 'cancelled'].includes(String(imageTask.value?.status)); attempt += 1) {
        await new Promise(resolve => window.setTimeout(resolve, 2000))
        imageTask.value = await getCreativeImageTask(apiKey.key, taskId)
      }
    }
    if (imageTask.value?.status === 'completed') {
      creationRecords.value.unshift({
        id: imageTask.value.id || `image_${Date.now()}`,
        type: 'image',
        statusKey: 'completed',
        icon: 'grid',
        title: imageForm.value.prompt.trim(),
        description: `${imageForm.value.model} · ${imageForm.value.size}`,
        meta: new Date().toLocaleString(),
        status: t('creativeCenter.sections.image.status'),
        action: t('creativeCenter.sections.history.openImage'),
        pending: false,
      })
    }
  } catch (error: any) {
    imageError.value = error?.message || t('creativeCenter.workspace.form.submitFailed')
  } finally {
    imageSubmitting.value = false
  }
}

async function generateVideo() {
  videoError.value = ''
  if (!videoForm.value.prompt.trim()) {
    videoError.value = t('creativeCenter.workspace.form.promptRequired')
    return
  }
  const apiKey = apiKeys.value.find(key => String(key.id) === videoForm.value.apiKeyId)
  if (!apiKey?.key) {
    videoError.value = t('creativeCenter.workspace.form.keyRequired')
    return
  }
  videoSubmitting.value = true
  videoTask.value = null
  try {
    videoTask.value = await createCreativeVideo(apiKey.key, {
      model: videoForm.value.model,
      prompt: videoForm.value.prompt.trim(),
      seconds: Number(videoForm.value.duration),
      size: videoForm.value.size,
      ...(videoForm.value.input_references.length ? { input_reference: videoForm.value.input_references } : {}),
    })
    creationRecords.value.unshift({
      id: videoTask.value.id || `video_${Date.now()}`,
      type: 'video',
      statusKey: 'pending',
      icon: 'play',
      title: videoForm.value.prompt.trim(),
      description: `${videoForm.value.model} · ${videoForm.value.duration}s`,
      meta: new Date().toLocaleString(),
      status: videoTask.value.status || t('creativeCenter.sections.history.pending'),
      action: '',
      pending: true,
    })
  } catch (error: any) {
    videoError.value = error?.message || t('creativeCenter.workspace.form.submitFailed')
  } finally {
    videoSubmitting.value = false
  }
}

onMounted(async () => {
  try {
    const response = await keysAPI.list(1, 100, { status: 'active', sort_by: 'created_at', sort_order: 'desc' })
    apiKeys.value = response.items || []
    const first = apiKeys.value[0]
    if (first) {
      imageForm.value.apiKeyId = String(first.id)
      videoForm.value.apiKeyId = String(first.id)
    }
  } catch {
    // The form remains usable and shows a translated key-required message on submit.
  }
})

const imageParameters = computed<CreativeWorkspaceParameter[]>(() => [
  {
    label: t('creativeCenter.workspace.parameters.model'),
    value: t('creativeCenter.workspace.image.modelValue'),
    hint: t('creativeCenter.workspace.image.modelHint'),
  },
  {
    label: t('creativeCenter.workspace.parameters.prompt'),
    value: t('creativeCenter.workspace.image.promptValue'),
  },
  {
    label: t('creativeCenter.workspace.parameters.size'),
    value: t('creativeCenter.workspace.image.sizeValue'),
  },
  {
    label: t('creativeCenter.workspace.parameters.quality'),
    value: t('creativeCenter.workspace.image.qualityValue'),
  },
  {
    label: t('creativeCenter.workspace.parameters.count'),
    value: t('creativeCenter.workspace.image.countValue'),
  },
  {
    label: t('creativeCenter.workspace.parameters.responseFormat'),
    value: t('creativeCenter.workspace.image.responseFormatValue'),
  },
])

const videoParameters = computed<CreativeWorkspaceParameter[]>(() => [
  {
    label: t('creativeCenter.workspace.parameters.model'),
    value: t('creativeCenter.workspace.video.modelValue'),
    hint: t('creativeCenter.workspace.video.modelHint'),
  },
  {
    label: t('creativeCenter.workspace.parameters.prompt'),
    value: t('creativeCenter.workspace.video.promptValue'),
  },
  {
    label: t('creativeCenter.workspace.parameters.duration'),
    value: t('creativeCenter.workspace.video.durationValue'),
  },
  {
    label: t('creativeCenter.workspace.parameters.size'),
    value: t('creativeCenter.workspace.video.sizeValue'),
  },
  {
    label: t('creativeCenter.workspace.parameters.inputReference'),
    value: t('creativeCenter.workspace.video.inputReferenceValue'),
  },
])

const historyFilter = ref({ keyword: '', type: 'all', status: 'all' })

type CreationRecord = {
  id: string
  type: 'image' | 'video'
  statusKey: 'completed' | 'pending'
  icon: 'grid' | 'play'
  title: string
  description: string
  meta: string
  status: string
  action: string
  pending: boolean
}

const creationRecords = ref<CreationRecord[]>([])
const historyItems = computed(() => creationRecords.value)

const filteredHistoryItems = computed(() => historyItems.value.filter((item) => {
  const keyword = historyFilter.value.keyword.trim().toLowerCase()
  const matchesKeyword = !keyword || `${item.title} ${item.description} ${item.meta}`.toLowerCase().includes(keyword)
  const matchesType = historyFilter.value.type === 'all' || historyFilter.value.type === item.type
  const matchesStatus = historyFilter.value.status === 'all' || historyFilter.value.status === item.statusKey
  return matchesKeyword && matchesType && matchesStatus
}))

function openImageWorkspace() {
  void router.push('/creative/image')
}
</script>

<template>
  <AppLayout>
    <div class="creative-center-page mx-auto max-w-[1600px]">
      <div class="creative-center-shell">
        <main class="creative-center-content">
          <section v-if="activeSection === 'image'" class="creative-content-section">
            <CreativeSplitWorkspace
              :eyebrow="t('creativeCenter.sections.image.eyebrow')"
              :title="t('creativeCenter.sections.image.title')"
              :description="t('creativeCenter.sections.image.description')"
              :result-title="t('creativeCenter.workspace.resultTitle')"
              :result-description="t('creativeCenter.workspace.image.resultDescription')"
              :parameters-title="t('creativeCenter.workspace.parameters.title')"
              :parameters="imageParameters"
              :protocol-endpoint="'/v1/images/generations'"
              :billing-title="t('creativeCenter.workspace.billing.title')"
              :billing-estimate="imageCostEstimate"
              :billing-description="t('creativeCenter.workspace.billing.description')"
              :storage-title="t('creativeCenter.workspace.storage.title')"
              :storage-value="t('creativeCenter.workspace.storage.value')"
              :storage-description="t('creativeCenter.workspace.storage.description')"
              :status="t('creativeCenter.sections.image.status')"
              :submitting="imageSubmitting"
              :action-disabled="!imageForm.prompt.trim()"
            >
              <template #parameters>
                <div class="creative-form-fields mt-3 space-y-3">
                  <label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.apiKey') }}</span><select v-model="imageForm.apiKeyId" class="input"><option value="">{{ t('creativeCenter.workspace.form.keyPlaceholder') }}</option><option v-for="key in apiKeys" :key="key.id" :value="String(key.id)">{{ key.name || `#${key.id}` }}</option></select></label>
                  <label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.model') }}</span><select v-model="imageForm.model" class="input"><option value="gpt-image-1">gpt-image-1</option><option value="gpt-image-1-mini">gpt-image-1-mini</option></select></label>
                  <label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.prompt') }} <em>*</em></span><textarea v-model="imageForm.prompt" rows="4" class="input resize-y" :placeholder="t('creativeCenter.workspace.form.promptPlaceholder')" /></label>
                  <div class="grid grid-cols-2 gap-2"><label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.size') }}</span><select v-model="imageForm.size" class="input"><option>1024x1024</option><option>1536x1024</option><option>1024x1536</option></select></label><label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.quality') }}</span><select v-model="imageForm.quality" class="input"><option>auto</option><option>standard</option><option>hd</option></select></label></div>
                  <div class="grid grid-cols-2 gap-2"><label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.count') }}</span><select v-model.number="imageForm.n" class="input"><option :value="1">1</option><option :value="2">2</option><option :value="3">3</option><option :value="4">4</option></select></label><label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.responseFormat') }}</span><select v-model="imageForm.response_format" class="input"><option value="url">url</option><option value="b64_json">b64_json</option></select></label></div>
                </div>
              </template>
              <template #action><button type="button" class="btn btn-primary w-full justify-center" :disabled="imageSubmitting || !imageForm.prompt.trim()" @click="generateImage"><Icon :name="imageSubmitting ? 'refresh' : 'sparkles'" size="sm" :class="imageSubmitting ? 'animate-spin' : ''" /><span>{{ imageSubmitting ? t('creativeCenter.workspace.form.generating') : t('creativeCenter.workspace.form.generateImage') }}</span></button></template>
              <template #results>
                <div v-if="imageError" class="creative-error-card">{{ imageError }}</div>
                <div v-else-if="imageTask" class="creative-result-card">
                  <div class="flex items-center gap-2 text-sm font-semibold text-emerald-700 dark:text-emerald-300"><span class="h-2 w-2 rounded-full bg-emerald-500" />{{ t('creativeCenter.workspace.form.queued') }}</div>
                  <p v-if="imageTask.id" class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('creativeCenter.workspace.form.taskId') }}：{{ imageTask.id }}</p>
                  <div v-if="imageTask.result?.data?.length" class="mt-4 grid gap-3 sm:grid-cols-2"><img v-for="(item, index) in imageTask.result.data" :key="index" :src="item.url || (item.b64_json ? `data:image/png;base64,${item.b64_json}` : '')" class="aspect-square w-full rounded-xl object-cover" alt="generated result" /></div>
                  <img v-else-if="imageTask.image_url" :src="imageTask.image_url" class="mt-4 max-h-[520px] w-full rounded-xl object-contain" alt="generated result" />
                </div>
                <div v-else class="creative-result-empty">
                  <div class="creative-result-empty-icon bg-primary-100 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300">
                    <Icon name="grid" size="xl" />
                  </div>
                  <h3 class="mt-5 text-xl font-bold text-gray-900 dark:text-white">
                    {{ t('creativeCenter.workspace.image.emptyTitle') }}
                  </h3>
                  <p class="mx-auto mt-3 max-w-xl text-sm leading-6 text-gray-500 dark:text-gray-400">
                    {{ t('creativeCenter.workspace.image.emptyDescription') }}
                  </p>
                  <div class="mt-8 grid w-full max-w-2xl gap-3 text-left sm:grid-cols-3">
                    <div class="creative-result-capability">
                      <Icon name="clock" size="sm" class="text-primary-600 dark:text-primary-300" />
                      <p class="mt-3 text-xs font-medium leading-5 text-gray-700 dark:text-gray-200">{{ t('creativeCenter.workspace.image.capabilities.status') }}</p>
                    </div>
                    <div class="creative-result-capability">
                      <Icon name="eye" size="sm" class="text-primary-600 dark:text-primary-300" />
                      <p class="mt-3 text-xs font-medium leading-5 text-gray-700 dark:text-gray-200">{{ t('creativeCenter.workspace.image.capabilities.preview') }}</p>
                    </div>
                    <div class="creative-result-capability">
                      <Icon name="download" size="sm" class="text-primary-600 dark:text-primary-300" />
                      <p class="mt-3 text-xs font-medium leading-5 text-gray-700 dark:text-gray-200">{{ t('creativeCenter.workspace.image.capabilities.download') }}</p>
                    </div>
                  </div>
                </div>
              </template>
            </CreativeSplitWorkspace>
          </section>

          <section v-else-if="activeSection === 'video'" class="creative-content-section">
            <CreativeSplitWorkspace
              :eyebrow="t('creativeCenter.sections.video.eyebrow')"
              :title="t('creativeCenter.sections.video.title')"
              :description="t('creativeCenter.sections.video.description')"
              :result-title="t('creativeCenter.workspace.resultTitle')"
              :result-description="t('creativeCenter.workspace.video.resultDescription')"
              :parameters-title="t('creativeCenter.workspace.parameters.title')"
              :parameters="videoParameters"
              :protocol-endpoint="'/v1/videos/generations'"
              :billing-title="t('creativeCenter.workspace.billing.title')"
              :billing-estimate="videoCostEstimate"
              :billing-description="t('creativeCenter.workspace.billing.description')"
              :storage-title="t('creativeCenter.workspace.storage.title')"
              :storage-value="t('creativeCenter.workspace.storage.value')"
              :storage-description="t('creativeCenter.workspace.storage.description')"
              :status="t('creativeCenter.sections.video.comingSoon')"
              :submitting="videoSubmitting"
              :action-disabled="!videoForm.prompt.trim()"
              accent="violet"
            >
              <template #parameters>
                <div class="creative-form-fields mt-3 space-y-3">
                  <label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.apiKey') }}</span><select v-model="videoForm.apiKeyId" class="input"><option value="">{{ t('creativeCenter.workspace.form.keyPlaceholder') }}</option><option v-for="key in apiKeys" :key="key.id" :value="String(key.id)">{{ key.name || `#${key.id}` }}</option></select></label>
                  <label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.model') }}</span><select v-model="videoForm.model" class="input"><option value="sora-1">sora-1</option><option value="grok-imagine-video">grok-imagine-video</option></select></label>
                  <label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.prompt') }} <em>*</em></span><textarea v-model="videoForm.prompt" rows="4" class="input resize-y" :placeholder="t('creativeCenter.workspace.form.promptPlaceholder')" /></label>
                  <div class="grid grid-cols-2 gap-2"><label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.duration') }}</span><select v-model.number="videoForm.duration" class="input"><option :value="5">5s</option><option :value="10">10s</option></select></label><label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.size') }}</span><select v-model="videoForm.size" class="input"><option>1280x720</option><option>720x1280</option></select></label></div>
                  <div class="creative-field">
                    <span>{{ t('creativeCenter.workspace.parameters.inputReference') }}</span>
                    <input id="creative-video-reference" type="file" accept="image/png,image/jpeg,image/webp" multiple class="sr-only" @change="handleVideoReferenceUpload" />
                    <label for="creative-video-reference" class="creative-upload-box">
                      <div v-if="videoReferencePreviews.length" class="grid w-full grid-cols-3 gap-2">
                        <div v-for="(preview, index) in videoReferencePreviews" :key="preview" class="relative">
                          <img :src="preview" class="creative-upload-preview" alt="" />
                          <button type="button" class="absolute right-1 top-1 rounded-full bg-black/60 px-1.5 text-xs text-white" @click.prevent="removeVideoReference(index)">×</button>
                        </div>
                      </div>
                      <template v-else>
                        <Icon name="upload" size="md" class="text-violet-500" />
                        <span class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('creativeCenter.workspace.form.uploadReference') }}</span>
                        <span class="mt-1 text-xs text-gray-400 dark:text-gray-500">PNG / JPG / WEBP</span>
                      </template>
                    </label>
                    <button v-if="videoReferencePreviews.length" type="button" class="mt-2 text-xs text-gray-500 hover:text-red-600 dark:text-gray-400" @click="clearVideoReferences">
                      {{ t('creativeCenter.workspace.form.removeReference') }}
                    </button>
                  </div>
                </div>
              </template>
              <template #action><button type="button" class="btn btn-secondary w-full justify-center border-violet-200 text-violet-700 hover:border-violet-300 dark:border-violet-800 dark:text-violet-300" :disabled="videoSubmitting || !videoForm.prompt.trim()" @click="generateVideo"><Icon :name="videoSubmitting ? 'refresh' : 'play'" size="sm" :class="videoSubmitting ? 'animate-spin' : ''" /><span>{{ videoSubmitting ? t('creativeCenter.workspace.form.generating') : t('creativeCenter.workspace.form.generateVideo') }}</span></button></template>
              <template #results>
                <div v-if="videoError" class="creative-error-card">{{ videoError }}</div>
                <div v-else-if="videoTask" class="creative-result-card">
                  <div class="flex items-center gap-2 text-sm font-semibold text-violet-700 dark:text-violet-300"><span class="h-2 w-2 rounded-full bg-violet-500" />{{ t('creativeCenter.workspace.form.queued') }}</div>
                  <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('creativeCenter.workspace.form.taskId') }}：{{ videoTask.id || '—' }} · {{ videoTask.status || 'queued' }}</p>
                </div>
                <div v-else class="creative-placeholder-card">
                  <div class="creative-placeholder-icon bg-violet-100 text-violet-600 dark:bg-violet-900/30 dark:text-violet-300">
                    <Icon name="play" size="xl" />
                  </div>
                  <h3 class="mt-5 text-xl font-bold text-gray-900 dark:text-white">
                    {{ t('creativeCenter.workspace.video.emptyTitle') }}
                  </h3>
                  <p class="mx-auto mt-3 max-w-xl text-sm leading-6 text-gray-500 dark:text-gray-400">
                    {{ t('creativeCenter.workspace.video.emptyDescription') }}
                  </p>
                  <div class="mt-8 grid w-full max-w-2xl gap-3 text-left sm:grid-cols-3">
                    <div class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
                      <Icon name="sparkles" size="sm" class="text-primary-600 dark:text-primary-300" />
                      <p class="mt-3 text-xs font-medium leading-5 text-gray-700 dark:text-gray-200">{{ t('creativeCenter.sections.video.plannedItems.textToVideo') }}</p>
                    </div>
                    <div class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
                      <Icon name="clock" size="sm" class="text-primary-600 dark:text-primary-300" />
                      <p class="mt-3 text-xs font-medium leading-5 text-gray-700 dark:text-gray-200">{{ t('creativeCenter.sections.video.plannedItems.asyncTasks') }}</p>
                    </div>
                    <div class="rounded-xl border border-gray-200 bg-white p-4 dark:border-dark-700 dark:bg-dark-800">
                      <Icon name="eye" size="sm" class="text-primary-600 dark:text-primary-300" />
                      <p class="mt-3 text-xs font-medium leading-5 text-gray-700 dark:text-gray-200">{{ t('creativeCenter.sections.video.plannedItems.preview') }}</p>
                    </div>
                  </div>
                </div>
              </template>
            </CreativeSplitWorkspace>
          </section>

          <section v-else class="creative-content-section">
            <div class="mb-4 flex flex-col gap-3 rounded-2xl border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800 sm:flex-row">
              <input v-model="historyFilter.keyword" class="input min-w-0 flex-1" :placeholder="t('creativeCenter.sections.history.filters.keyword')" />
              <select v-model="historyFilter.type" class="input sm:w-40">
                <option value="all">{{ t('creativeCenter.sections.history.filters.allTypes') }}</option>
                <option value="image">{{ t('creativeCenter.sections.history.filters.image') }}</option>
                <option value="video">{{ t('creativeCenter.sections.history.filters.video') }}</option>
              </select>
              <select v-model="historyFilter.status" class="input sm:w-40">
                <option value="all">{{ t('creativeCenter.sections.history.filters.allStatuses') }}</option>
                <option value="completed">{{ t('creativeCenter.sections.history.filters.completed') }}</option>
                <option value="pending">{{ t('creativeCenter.sections.history.filters.pending') }}</option>
              </select>
            </div>

            <ul class="creative-history-list" role="list">
              <li v-for="item in filteredHistoryItems" :key="item.id" class="creative-history-item">
                <div class="creative-history-icon" :class="item.pending ? 'creative-history-icon-pending' : 'creative-history-icon-ready'">
                  <Icon :name="item.icon" size="md" />
                </div>
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-2">
                    <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ item.title }}</h3>
                    <span
                      class="rounded-full px-2.5 py-1 text-xs font-medium"
                      :class="item.pending ? 'bg-gray-100 text-gray-500 dark:bg-dark-700 dark:text-gray-400' : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300'"
                    >
                      {{ item.status }}
                    </span>
                  </div>
                  <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">{{ item.description }}</p>
                  <p class="mt-2 text-xs text-gray-400 dark:text-gray-500">{{ item.meta }}</p>
                </div>
                <button v-if="item.action" type="button" class="btn btn-secondary flex-shrink-0" @click="openImageWorkspace">
                  {{ item.action }}
                  <Icon name="arrowRight" size="sm" />
                </button>
              </li>
              <li v-if="filteredHistoryItems.length === 0" class="p-10 text-center text-sm text-gray-500 dark:text-gray-400">
                {{ t('creativeCenter.sections.history.filters.empty') }}
              </li>
            </ul>
          </section>
        </main>
      </div>
    </div>
  </AppLayout>
</template>

<style scoped>
.creative-center-shell {
  @apply flex min-h-[680px] flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-800;
}

.creative-center-content {
  @apply flex min-h-0 min-w-0 flex-1 w-full bg-gray-50/50 dark:bg-dark-950/20;
}

.creative-content-section {
  @apply flex h-full min-h-0 w-full min-w-0 flex-1 flex-col p-5 md:p-6;
}

.creative-content-header {
  @apply mb-5 flex flex-shrink-0 flex-col gap-3 sm:flex-row sm:items-start sm:justify-between;
}

.creative-form-fields :deep(.input) {
  @apply mt-1 w-full;
}

.creative-field {
  @apply block text-xs font-medium text-gray-600 dark:text-gray-300;
}

.creative-field em {
  @apply not-italic text-red-500;
}

.creative-error-card {
  @apply rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-900/20 dark:text-red-300;
}

.creative-result-card {
  @apply min-h-[520px] flex-1 rounded-2xl border border-gray-200 bg-white p-5 dark:border-dark-700 dark:bg-dark-800;
}

.creative-placeholder-card {
  @apply flex min-h-[560px] flex-1 flex-col items-center justify-center rounded-2xl border border-gray-200 bg-white p-6 text-center dark:border-dark-700 dark:bg-dark-800;
}

.creative-placeholder-icon {
  @apply flex h-16 w-16 items-center justify-center rounded-2xl;
}

.creative-result-empty {
  @apply flex min-h-[520px] flex-1 flex-col items-center justify-center rounded-2xl border border-dashed border-gray-300 bg-white p-6 text-center dark:border-dark-600 dark:bg-dark-800/60;
}

.creative-result-empty-icon {
  @apply flex h-16 w-16 items-center justify-center rounded-2xl;
}

.creative-result-capability {
  @apply rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-dark-700 dark:bg-dark-800;
}

.creative-field {
  @apply block text-xs font-medium text-gray-600 dark:text-gray-300;
}

.creative-field > span {
  @apply mb-1.5 block;
}

.creative-field em {
  @apply not-italic text-red-500;
}

.creative-field .input {
  @apply w-full text-sm;
}

.creative-error-card {
  @apply rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-700 dark:border-red-900/50 dark:bg-red-950/30 dark:text-red-300;
}

.creative-result-card {
  @apply rounded-2xl border border-emerald-200 bg-white p-5 dark:border-emerald-900/50 dark:bg-dark-800;
}

.creative-upload-box {
  @apply mt-1 flex min-h-32 cursor-pointer flex-col items-center justify-center rounded-xl border border-dashed border-gray-300 bg-white p-4 text-center transition-colors hover:border-violet-400 hover:bg-violet-50/50 dark:border-dark-600 dark:bg-dark-800 dark:hover:border-violet-500 dark:hover:bg-violet-900/10;
}

.creative-upload-preview {
  @apply h-28 w-full rounded-lg object-contain;
}

.creative-history-list {
  @apply divide-y divide-gray-200 overflow-hidden rounded-2xl border border-gray-200 bg-white dark:divide-dark-700 dark:border-dark-700 dark:bg-dark-800;
}

.creative-history-item {
  @apply flex items-center gap-4 p-4 transition-colors hover:bg-gray-50 dark:hover:bg-dark-700/40;
}

.creative-history-icon {
  @apply flex h-11 w-11 flex-shrink-0 items-center justify-center rounded-xl;
}

.creative-history-icon-ready {
  @apply bg-emerald-100 text-emerald-600 dark:bg-emerald-900/30 dark:text-emerald-300;
}

.creative-history-icon-pending {
  @apply bg-violet-100 text-violet-600 dark:bg-violet-900/30 dark:text-violet-300;
}

@media (max-width: 640px) {
  .creative-history-item {
    @apply flex-wrap items-start;
  }

  .creative-history-item .btn {
    @apply ml-[3.75rem] w-[calc(100%-3.75rem)] justify-center;
  }
}
</style>
