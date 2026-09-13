<script setup lang="ts">
import { computed, ref, onMounted, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import Icon from '@/components/icons/Icon.vue'
import { keysAPI, userGroupsAPI } from '@/api'
import { userChannelsAPI, type UserAvailableChannel } from '@/api/channels'
import { generateCreativeImage, editCreativeImage, submitCreativeImageTask, getCreativeImageTask, createCreativeVideo, getCreativeVideo, listCreativeVideos, listCreativeModels, listCreativeVideoModels, type CreativeImageTask, type CreativeModel } from '@/api/creative'
import type { ApiKey, Group } from '@/types'
import CreativeSplitWorkspace, { type CreativeWorkspaceParameter } from '@/components/creative/CreativeSplitWorkspace.vue'

type CreativeSection = 'image' | 'video' | 'history'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const activeSection = computed<CreativeSection>(() => {
  const section = route.path.split('/')[2]
  return section === 'video' || section === 'history' ? section : 'image'
})

const imageForm = ref({ apiKeyId: '', model: 'jiaotu-image-v2', prompt: '', size: '1024x1024', quality: 'auto', n: 1, response_format: 'url' })
const imageReferenceFiles = ref<File[]>([])
const imageReferencePreviews = ref<string[]>([])
const videoForm = ref({ apiKeyId: '', model: 'sora-1', prompt: '', duration: 5, size: '1280x720', input_references: [] as string[] })
const videoReferencePreviews = ref<string[]>([])
const apiKeys = ref<ApiKey[]>([])
const imageSubmitting = ref(false)
const videoSubmitting = ref(false)
const imageError = ref('')
const videoError = ref('')
const imageTask = ref<CreativeImageTask | null>(null)
const videoTask = ref<{ id?: string, status?: string, url?: string, model?: string, progress?: number, error?: { message?: string } | null } | null>(null)
const videoPolling = ref(false)
const availableChannels = ref<UserAvailableChannel[]>([])
const availableGroups = ref<Group[]>([])
const availableImageModels = ref<CreativeModel[]>([])
const availableVideoModels = ref<CreativeModel[]>([])
const keyCreating = ref(false)

const jiaotuImageModel = { id: 'jiaotu-image-v2', name: '全能图片 V2', provider: '椒图号池' }

// 创作中心的上游既可能是原生 `platform=jiaotu` 分组（直连椒图号池），
// 也可能是旧的 `platform=openai` 兼容桥接分组。两者入站协议相同，都要认，
// 否则原生分组会被过滤掉，导致「创建密钥」按钮禁用、模型下拉为空。
const CREATIVE_GROUP_PLATFORMS = new Set(['jiaotu', 'openai'])

function isCreativeGroupPlatform(platform: string): boolean {
  return CREATIVE_GROUP_PLATFORMS.has(platform)
}

const imageModels = computed(() => {
  const models = availableImageModels.value.filter((item) => item.id !== 'gpt-image-1-mini')
  if (models.length) return models
  const channelModels: CreativeModel[] = availableChannels.value
    .flatMap((channel) => channel.platforms)
    .filter((section) => isCreativeGroupPlatform(section.platform) && (!imageGroup.value || section.groups.some((group) => group.id === imageGroup.value?.id)))
    .flatMap((section) => section.supported_models)
    .filter((model) => model.name === 'gpt-image-1' || model.name.startsWith('jiaotu-'))
    .filter((model, index, all) => all.findIndex((candidate) => candidate.name === model.name) === index)
    .map((model) => ({
      id: model.name,
      name: model.name === 'gpt-image-1' ? 'OpenAI Images 兼容入口' : model.name,
      provider: 'jiaotu',
      provider_model_name: model.name,
    }))
  if (channelModels.length) return channelModels
  return [{ id: jiaotuImageModel.id, name: jiaotuImageModel.name, provider: jiaotuImageModel.provider, provider_model_name: jiaotuImageModel.name }]
})

function preferredImageModelId(models: CreativeModel[]) {
  return models.find((model) => model.id === 'jiaotu-image-v2')?.id
    || models.find((model) => model.id === 'gpt-image-1')?.id
    || models[0]?.id
    || jiaotuImageModel.id
}

watch(imageModels, (models) => {
  if (models.length && !models.some((model) => model.id === imageForm.value.model)) {
    imageForm.value.model = preferredImageModelId(models)
  }
}, { immediate: true })

const selectedImageModel = computed(() => imageModels.value.find((item) => item.id === imageForm.value.model) || imageModels.value[0])
const imageProtocolEndpoint = computed(() => imageReferenceFiles.value.length ? '/v1/images/edits' : '/v1/images/generations')

const imageCostEstimate = computed(() => t('creativeCenter.workspace.billing.byGroup'))
const videoCostEstimate = computed(() => `$${(Number(videoForm.value.duration) * 0.18).toFixed(2)} 起`)

let modelRefreshSequence = 0
async function refreshImageModels(apiKeyId: string) {
  const sequence = ++modelRefreshSequence
  const key = imageApiKeys.value.find((item) => String(item.id) === apiKeyId)?.key
  if (!key) {
    availableImageModels.value = []
    imageForm.value.model = jiaotuImageModel.id
    return
  }
  try {
    const models = await listCreativeModels(key)
    if (sequence !== modelRefreshSequence) return
    availableImageModels.value = models
    const current = models.find((model) => model.id === imageForm.value.model)
    if (!current) {
      imageForm.value.model = preferredImageModelId(models)
    }
  } catch {
    if (sequence === modelRefreshSequence) {
      availableImageModels.value = []
      imageForm.value.model = jiaotuImageModel.id
    }
  }
}

watch(() => imageForm.value.apiKeyId, (apiKeyId) => {
  void refreshImageModels(apiKeyId)
})

// 视频侧模型：/v1/models 按 capability=video 过滤，拿不到时退回原有的写死选项，
// 保证未接入椒图视频分组的实例上创作中心行为不变。
const videoModelFallback = computed<CreativeModel[]>(() => [
  { id: 'sora-1', name: 'sora-1' },
  { id: 'grok-imagine-video', name: 'grok-imagine-video' },
])
const videoModels = computed<CreativeModel[]>(() => availableVideoModels.value.length ? availableVideoModels.value : videoModelFallback.value)

let videoModelRefreshSequence = 0
async function refreshVideoModels(apiKeyId: string) {
  const sequence = ++videoModelRefreshSequence
  const key = apiKeys.value.find((item) => String(item.id) === apiKeyId)?.key
  if (!key) {
    availableVideoModels.value = []
    return
  }
  try {
    const models = await listCreativeVideoModels(key)
    if (sequence !== videoModelRefreshSequence) return
    availableVideoModels.value = models
    if (models.length && !models.some((model) => model.id === videoForm.value.model)) {
      videoForm.value.model = models[0]!.id
    }
  } catch {
    if (sequence === videoModelRefreshSequence) availableVideoModels.value = []
  }
}

watch(() => videoForm.value.apiKeyId, (apiKeyId) => {
  void refreshVideoModels(apiKeyId)
})

const eligibleImageGroups = computed(() => availableGroups.value
  .filter((group) => isCreativeGroupPlatform(group.platform) && group.status === 'active' && group.allow_image_generation))

const imageGroup = computed(() => {
  const eligible = eligibleImageGroups.value
  const channelSections = availableChannels.value
    .flatMap((channel) => channel.platforms)
    .filter((section) => isCreativeGroupPlatform(section.platform))
  const jiaotuGroupIds = new Set(channelSections
    .filter((section) => section.supported_models.some((model) => model.name.startsWith('jiaotu-') || model.name === 'gpt-image-1'))
    .flatMap((section) => section.groups.map((group) => group.id)))
  const channelGroupIds = new Set(channelSections.flatMap((section) => section.groups.map((group) => group.id)))

  // /groups/available is authoritative; channel groups only narrow the
  // candidate when the catalogue explicitly advertises a Jiaotu image model.
  // 原生 `platform=jiaotu` 优先于 openai 兼容分组：前者直连椒图号池，
  // 后者是历史桥接路径。
  return eligible.find((group) => group.platform === 'jiaotu' && group.name.includes('椒图'))
    || eligible.find((group) => group.platform === 'jiaotu' && jiaotuGroupIds.has(group.id))
    || eligible.find((group) => group.platform === 'jiaotu')
    || eligible.find((group) => jiaotuGroupIds.has(group.id) && group.name.includes('椒图'))
    || eligible.find((group) => jiaotuGroupIds.has(group.id))
    || eligible.find((group) => group.name.includes('椒图') && channelGroupIds.has(group.id))
    || eligible.find((group) => group.name.includes('椒图'))
    || eligible.find((group) => channelGroupIds.has(group.id))
    || eligible[0]
})

const imageApiKeys = computed(() => {
  const groupId = imageGroup.value?.id
  return groupId == null ? [] : apiKeys.value.filter((key) => key.status === 'active' && Number(key.group_id) === Number(groupId))
})

watch(imageApiKeys, (keys) => {
  if (!keys.some((key) => String(key.id) === imageForm.value.apiKeyId)) {
    imageForm.value.apiKeyId = keys[0] ? String(keys[0].id) : ''
  }
}, { immediate: true })

async function createCreativeKey() {
  if (keyCreating.value || !imageGroup.value) return
  keyCreating.value = true
  imageError.value = ''
  try {
    const created = await keysAPI.create('椒图创作密钥', imageGroup.value.id)
    apiKeys.value = [created, ...apiKeys.value.filter((key) => key.id !== created.id)]
    imageForm.value.apiKeyId = String(created.id)
    try {
      availableImageModels.value = await listCreativeModels(created.key)
      const preferred = availableImageModels.value.find((model) => model.id === 'jiaotu-image-v2') || availableImageModels.value.find((model) => model.id === 'gpt-image-1')
      if (preferred) imageForm.value.model = preferred.id
    } catch {
      // Model discovery may be blocked by a zero balance even though key
      // creation succeeded. The channel catalogue remains available as the
      // model fallback, so do not report a misleading key-creation failure.
      availableImageModels.value = []
    }
  } catch (error: any) {
    imageError.value = error?.message || t('creativeCenter.workspace.form.keyCreateFailed')
  } finally {
    keyCreating.value = false
  }
}

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

function handleImageReferenceUpload(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  if (!files.length) return
  const invalid = files.find(file => !['image/png', 'image/jpeg', 'image/webp'].includes(file.type))
  if (invalid) {
    imageError.value = t('creativeCenter.workspace.form.referenceImageOnly')
    return
  }
  const oversized = files.find(file => file.size > 20 * 1024 * 1024)
  if (oversized) {
    imageError.value = t('creativeCenter.workspace.form.referenceImageTooLarge')
    return
  }
  const nextFiles = [...imageReferenceFiles.value, ...files].slice(0, 4)
  imageReferenceFiles.value = nextFiles
  imageReferencePreviews.value = nextFiles.map(file => URL.createObjectURL(file))
  imageError.value = ''
}

function removeImageReference(index: number) {
  imageReferenceFiles.value.splice(index, 1)
  imageReferencePreviews.value = imageReferenceFiles.value.map(file => URL.createObjectURL(file))
}

function clearImageReferences() {
  imageReferenceFiles.value = []
  imageReferencePreviews.value = []
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
  const apiKey = imageApiKeys.value.find(key => String(key.id) === imageForm.value.apiKeyId)
  if (!apiKey?.key) {
    imageError.value = t('creativeCenter.workspace.form.keyRequired')
    return
  }
  imageSubmitting.value = true
  imageTask.value = null
  try {
    let usedSynchronousFallback = false
    const payload = {
      model: imageForm.value.model,
      prompt: imageForm.value.prompt.trim(),
      size: imageForm.value.size,
      quality: imageForm.value.quality,
      n: Number(imageForm.value.n),
      response_format: imageForm.value.response_format,
    }
    if (imageReferenceFiles.value.length) {
      // The async endpoint accepts JSON only. Reference images use the
      // OpenAI-compatible multipart edits endpoint and therefore stay
      // synchronous so the uploaded files are forwarded in the same request.
      usedSynchronousFallback = true
      const response = await editCreativeImage(apiKey.key, payload, imageReferenceFiles.value)
      imageTask.value = { id: `sync_${Date.now()}`, status: 'completed', result: response }
    } else {
      try {
        imageTask.value = await submitCreativeImageTask(apiKey.key, payload)
      } catch (error: any) {
        // Async tasks require R2 to be enabled. Keep the workspace usable before
        // storage is configured by falling back to the synchronous OpenAI response.
        if (Number(error?.status) !== 404) throw error
        usedSynchronousFallback = true
        const response = await generateCreativeImage(apiKey.key, payload)
        imageTask.value = { id: `sync_${Date.now()}`, status: 'completed', result: response }
      }
    }
    // Poll the async task so the eventual R2 URL is shown without exposing provider storage.
    const taskId = imageTask.value?.id || imageTask.value?.task_id
    if (taskId && !usedSynchronousFallback && !['completed', 'failed', 'cancelled'].includes(String(imageTask.value?.status))) {
      for (let attempt = 0; attempt < 30 && !['completed', 'failed', 'cancelled'].includes(String(imageTask.value?.status)); attempt += 1) {
        await new Promise(resolve => window.setTimeout(resolve, 2000))
        imageTask.value = await getCreativeImageTask(apiKey.key, taskId)
      }
    }
    if (imageTask.value?.status === 'failed' || imageTask.value?.status === 'cancelled') return
    if (imageTask.value?.status === 'completed') {
      if (!imageTaskHasResult(imageTask.value)) {
        imageError.value = t('creativeCenter.workspace.form.resultMissing')
        return
      }
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
    imageError.value = error?.code === 'INSUFFICIENT_BALANCE'
      ? t('creativeCenter.workspace.form.balanceInsufficient')
      : error?.message || t('creativeCenter.workspace.form.submitFailed')
  } finally {
    imageSubmitting.value = false
  }
}

// 视频任务轮询：上游出片通常几十秒到几分钟，图片侧用 30 次 × 2s，
// 视频更慢，放宽到 120 次 × 3s（6 分钟）仍拿不到就停在 pending，
// 任务本身不会丢——结果在后端保留 24h，可通过创作记录/重新提交取回。
async function pollVideoTask(apiKey: string) {
  const taskId = videoTask.value?.id
  if (!taskId) return
  if (['completed', 'failed', 'cancelled'].includes(String(videoTask.value?.status))) return
  videoPolling.value = true
  try {
    for (let attempt = 0; attempt < 120; attempt += 1) {
      await new Promise(resolve => window.setTimeout(resolve, 3000))
      videoTask.value = await getCreativeVideo(apiKey, taskId)
      const status = String(videoTask.value?.status || '')
      if (['completed', 'failed', 'cancelled'].includes(status)) return
    }
  } catch (error: any) {
    // 轮询失败不吞掉已提交的任务：保留当前状态，用户可重试或查创作记录。
    if (!videoTask.value?.id) videoError.value = error?.message || t('creativeCenter.workspace.form.submitFailed')
  } finally {
    videoPolling.value = false
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
    // 视频是异步任务：必须轮询，否则任务成功了前端也永远停在“排队中”，
    // 刷新后内存记录一清空，用户看到的就是“什么都没有了”。
    await pollVideoTask(apiKey.key)
    if (videoTask.value?.status === 'completed' && videoTask.value?.url) {
      creationRecords.value.unshift({
        id: videoTask.value.id || `video_${Date.now()}`,
        type: 'video',
        statusKey: 'completed',
        icon: 'play',
        title: videoForm.value.prompt.trim(),
        description: `${videoForm.value.model} · ${videoForm.value.duration}s`,
        meta: new Date().toLocaleString(),
        status: t('creativeCenter.sections.image.status'),
        action: t('creativeCenter.sections.history.openVideo'),
        pending: false,
      })
      return
    }
    creationRecords.value.unshift({
      id: videoTask.value?.id || `video_${Date.now()}`,
      type: 'video',
      statusKey: 'pending',
      icon: 'play',
      title: videoForm.value.prompt.trim(),
      description: `${videoForm.value.model} · ${videoForm.value.duration}s`,
      meta: new Date().toLocaleString(),
      status: videoTask.value?.status || t('creativeCenter.sections.history.pending'),
      action: '',
      pending: true,
    })
  } catch (error: any) {
    videoError.value = error?.code === 'INSUFFICIENT_BALANCE'
      ? t('creativeCenter.workspace.form.balanceInsufficient')
      : error?.message || t('creativeCenter.workspace.form.submitFailed')
  } finally {
    videoSubmitting.value = false
  }
}

onMounted(async () => {
  const [channelsResult, groupsResult] = await Promise.allSettled([
    userChannelsAPI.getAvailable(),
    userGroupsAPI.getAvailable(),
  ])
  if (channelsResult.status === 'fulfilled') availableChannels.value = channelsResult.value
  if (groupsResult.status === 'fulfilled') availableGroups.value = groupsResult.value

  const keyFilters = { status: 'active' as const, sort_by: 'created_at', sort_order: 'desc' as const }
  const imageGroupId = imageGroup.value?.id
  const [keysResult, imageKeysResult] = await Promise.allSettled([
    keysAPI.list(1, 100, keyFilters),
    imageGroupId
      ? keysAPI.list(1, 100, { ...keyFilters, group_id: imageGroupId })
      : Promise.resolve({ items: [] as ApiKey[] }),
  ])
  const keyMap = new Map<number, ApiKey>()
  for (const result of [keysResult, imageKeysResult]) {
    if (result.status !== 'fulfilled') continue
    for (const key of result.value.items || []) keyMap.set(key.id, key)
  }
  apiKeys.value = Array.from(keyMap.values()).sort((a, b) => b.created_at.localeCompare(a.created_at))
  if (apiKeys.value.length) {
    const first = apiKeys.value[0]
    if (first) {
      videoForm.value.apiKeyId = String(first.id)
      await refreshVideoModels(String(first.id))
    }
    const imageKey = imageApiKeys.value[0]
    if (imageKey) {
      imageForm.value.apiKeyId = String(imageKey.id)
      await refreshImageModels(String(imageKey.id))
    }
  }
})

function imageDataUrl(item: { url?: string, b64_json?: string }) {
  return item.url || (item.b64_json ? `data:image/png;base64,${item.b64_json}` : '')
}

function imageTaskFailureMessage(task: CreativeImageTask) {
  const code = String(task.error?.code || task.error?.type || '').toUpperCase()
  const message = String(task.error?.message || '').toLowerCase()
  if (code.includes('INSUFFICIENT_BALANCE') || code.includes('BALANCE') || message.includes('insufficient balance') || message.includes('余额不足')) {
    return t('creativeCenter.workspace.form.balanceInsufficient')
  }
  if (task.status === 'cancelled') return t('creativeCenter.workspace.form.taskCancelled')
  return task.error?.message || t('creativeCenter.workspace.form.taskFailed')
}

function imageTaskHasResult(task: CreativeImageTask) {
  return Boolean(task.image_url || task.result?.data?.some((item) => imageDataUrl(item)))
}

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
  /** 视频完成态的可播放地址（后端保留 24h）；图片任务没有这个字段。 */
  url?: string
}

const creationRecords = ref<CreationRecord[]>([])
const historyLoading = ref(false)
const historyLoaded = ref(false)
const historyError = ref('')
const historyItems = computed(() => creationRecords.value)

// 把后端视频任务拉回创作记录：任务在后端保留 24h，但之前只有单查，
// 前端记录又是纯内存的，刷新后历史就“消失”了。
async function loadVideoHistory(apiKeyId?: string) {
  const apiKey = apiKeyId
    ? apiKeys.value.find(key => String(key.id) === apiKeyId)
    : apiKeys.value.find(key => key.status === 'active' && key.key)
  const key = apiKey?.key
  if (!key) return
  historyLoading.value = true
  historyError.value = ''
  try {
    const jobs = await listCreativeVideos(key, { limit: 50 })
    const existing = new Set(creationRecords.value.map(item => item.id))
    const restored: CreationRecord[] = []
    for (const job of jobs) {
      if (!job?.id || existing.has(job.id)) continue
      existing.add(job.id)
      const completed = job.status === 'completed' && typeof job.url === 'string' && job.url !== ''
      const failed = job.status === 'failed' || job.status === 'expired'
      restored.push({
        id: job.id,
        type: 'video',
        statusKey: completed ? 'completed' : 'pending',
        icon: 'play',
        // 任务存储未持久化 prompt，标题用模型名 + 规格，避免编造用户输入。
        title: `${job.model || 'video'} · ${job.resolution || ''}`.trim(),
        description: completed
          ? `${job.resolution || ''} · ${job.size || ''}${job.duration ? ` · ${job.duration}s` : ''}`.trim()
          : job.status || '',
        meta: new Date((job.created_at || 0) * 1000).toLocaleString(),
        status: completed
          ? t('creativeCenter.sections.image.status')
          : failed
            ? t('creativeCenter.sections.history.videoFailed')
            : t('creativeCenter.sections.history.videoProcessing'),
        action: completed ? t('creativeCenter.sections.history.openVideo') : '',
        pending: !completed,
        url: completed ? String(job.url ?? '') || undefined : undefined,
      })
    }
    // 新任务排前，历史补齐在后
    creationRecords.value = [...restored, ...creationRecords.value]
    historyLoaded.value = true
  } catch (error: any) {
    historyError.value = error?.message || t('creativeCenter.sections.history.loadFailed')
  } finally {
    historyLoading.value = false
  }
}

watch([activeSection, () => apiKeys.value.length], ([section]) => {
  // apiKeys 也要等：进历史页时 keys 可能刚在拉，拿不到 key 就只能空手而归。
  if (section === 'history' && apiKeys.value.length && !historyLoaded.value && !historyLoading.value) {
    void loadVideoHistory()
  }
}, { immediate: true })

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
      <div class="creative-center-content">
          <section v-if="activeSection === 'image'" class="creative-content-section">
            <CreativeSplitWorkspace
              :eyebrow="t('creativeCenter.sections.image.eyebrow')"
              :title="t('creativeCenter.sections.image.title')"
              :description="t('creativeCenter.sections.image.description')"
              :result-title="t('creativeCenter.workspace.resultTitle')"
              :result-description="t('creativeCenter.workspace.image.resultDescription')"
              :parameters-title="t('creativeCenter.workspace.parameters.title')"
              :parameters="imageParameters"
              :protocol-endpoint="imageProtocolEndpoint"
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
                  <label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.apiKey') }}</span><select v-model="imageForm.apiKeyId" class="input"><option value="">{{ t('creativeCenter.workspace.form.keyPlaceholder') }}</option><option v-for="key in imageApiKeys" :key="key.id" :value="String(key.id)">{{ key.name || `#${key.id}` }}</option></select></label>
                  <div v-if="!imageApiKeys.length" class="creative-key-onboarding">
                    <p class="text-xs leading-5 text-gray-600 dark:text-gray-300">{{ t('creativeCenter.workspace.form.noKeyDescription') }}</p>
                    <button type="button" class="btn btn-secondary mt-2 w-full justify-center" :disabled="keyCreating || !imageGroup" @click="createCreativeKey">
                      <Icon :name="keyCreating ? 'refresh' : 'sparkles'" size="sm" :class="keyCreating ? 'animate-spin' : ''" />
                      {{ keyCreating ? t('creativeCenter.workspace.form.keyCreating') : t('creativeCenter.workspace.form.createKey') }}
                    </button>
                  </div>
                  <label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.model') }}</span><select v-model="imageForm.model" class="input"><option v-for="model in imageModels" :key="model.id" :value="model.id">{{ model.provider_model_name || model.display_name || model.name || model.id }}</option></select></label>
                  <label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.prompt') }} <em>*</em></span><textarea v-model="imageForm.prompt" rows="4" class="input resize-y" :placeholder="t('creativeCenter.workspace.form.promptPlaceholder')" /></label>
                  <div class="creative-field">
                    <span>{{ t('creativeCenter.workspace.parameters.inputReference') }}</span>
                    <input id="creative-image-reference" type="file" accept="image/png,image/jpeg,image/webp" multiple class="sr-only" @change="handleImageReferenceUpload" />
                    <label for="creative-image-reference" class="creative-upload-box">
                      <div v-if="imageReferencePreviews.length" class="grid w-full grid-cols-4 gap-2">
                        <div v-for="(preview, index) in imageReferencePreviews" :key="preview" class="relative">
                          <img :src="preview" class="creative-upload-preview" alt="" />
                          <button type="button" class="absolute right-1 top-1 rounded-full bg-black/60 px-1.5 text-xs text-white" @click.prevent="removeImageReference(index)">×</button>
                        </div>
                      </div>
                      <template v-else>
                        <Icon name="upload" size="md" class="text-violet-500" />
                        <span class="mt-2 text-sm text-gray-500 dark:text-gray-400">{{ t('creativeCenter.workspace.form.uploadReference') }}</span>
                        <span class="mt-1 text-xs text-gray-400 dark:text-gray-500">PNG / JPG / WEBP · {{ t('creativeCenter.workspace.form.referenceImageLimit') }}</span>
                      </template>
                    </label>
                    <button v-if="imageReferencePreviews.length" type="button" class="mt-2 text-xs text-gray-500 hover:text-red-600 dark:text-gray-400" @click="clearImageReferences">
                      {{ t('creativeCenter.workspace.form.removeReference') }}
                    </button>
                  </div>
                  <div class="grid grid-cols-2 gap-2"><label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.size') }}</span><select v-model="imageForm.size" class="input"><option>1024x1024</option><option>1536x1024</option><option>1024x1536</option></select></label><label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.quality') }}</span><select v-model="imageForm.quality" class="input"><option>auto</option><option>standard</option><option>hd</option></select></label></div>
                  <div class="grid grid-cols-2 gap-2"><label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.count') }}</span><select v-model.number="imageForm.n" class="input"><option :value="1">1</option><option :value="2">2</option><option :value="3">3</option><option :value="4">4</option></select></label><label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.responseFormat') }}</span><select v-model="imageForm.response_format" class="input"><option value="url">url</option><option value="b64_json">b64_json</option></select></label></div>
                </div>
              </template>
              <template #action><button type="button" class="btn btn-primary w-full justify-center" :disabled="imageSubmitting || !imageForm.apiKeyId || !imageForm.prompt.trim()" @click="generateImage"><Icon :name="imageSubmitting ? 'refresh' : 'sparkles'" size="sm" :class="imageSubmitting ? 'animate-spin' : ''" /><span>{{ imageSubmitting ? t('creativeCenter.workspace.form.generating') : t('creativeCenter.workspace.form.generateImage') }}</span></button></template>
              <template #results>
                <div v-if="imageError" class="creative-error-card">{{ imageError }}</div>
                <div v-else-if="imageTask" :class="['creative-result-card', { 'creative-result-card-error': imageTask.status === 'failed' || imageTask.status === 'cancelled' }]">
                  <div :class="['flex items-center justify-between gap-3 text-sm font-semibold', imageTask.status === 'failed' || imageTask.status === 'cancelled' ? 'text-red-700 dark:text-red-300' : 'text-emerald-700 dark:text-emerald-300']"><span class="flex items-center gap-2"><span :class="['h-2 w-2 rounded-full', imageTask.status === 'failed' || imageTask.status === 'cancelled' ? 'bg-red-500' : 'bg-emerald-500']" />{{ imageTask.status === 'completed' ? t('creativeCenter.workspace.form.completed') : imageTask.status === 'failed' ? t('creativeCenter.workspace.form.taskFailed') : imageTask.status === 'cancelled' ? t('creativeCenter.workspace.form.taskCancelled') : t('creativeCenter.workspace.form.queued') }}</span><span class="text-xs font-normal text-gray-500 dark:text-gray-400">{{ selectedImageModel?.provider_model_name || selectedImageModel?.display_name || selectedImageModel?.name || jiaotuImageModel.name }}</span></div>
                  <p v-if="imageTask.id" class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('creativeCenter.workspace.form.taskId') }}：{{ imageTask.id }}</p>
                  <p v-if="imageTask.status === 'failed' || imageTask.status === 'cancelled'" class="mt-4 rounded-xl bg-red-50 p-3 text-sm leading-6 text-red-700 dark:bg-red-950/30 dark:text-red-300">{{ imageTaskFailureMessage(imageTask) }}</p>
                  <div v-if="imageTask.result?.data?.length" class="creative-generated-images">
                    <div v-for="(item, index) in imageTask.result.data" :key="index" class="creative-generated-image group">
                      <img :src="imageDataUrl(item)" class="aspect-square w-full object-cover" alt="generated result" />
                      <a v-if="imageDataUrl(item)" :href="imageDataUrl(item)" :download="`jiaotu-${index + 1}.png`" class="absolute bottom-3 right-3 rounded-lg bg-gray-950/75 px-3 py-2 text-xs font-medium text-white opacity-0 transition group-hover:opacity-100 focus:opacity-100">{{ t('creativeCenter.workspace.form.downloadImage') }}</a>
                    </div>
                  </div>
                  <img v-else-if="imageTask.image_url" :src="imageTask.image_url" class="creative-generated-image-single mt-4 max-h-[520px] max-w-full rounded-xl object-contain" alt="generated result" />
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
                  <label class="creative-field"><span>{{ t('creativeCenter.workspace.parameters.model') }}</span><select v-model="videoForm.model" class="input" data-testid="creative-video-model"><option v-for="model in videoModels" :key="model.id" :value="model.id">{{ model.provider_model_name || model.display_name || model.name || model.id }}</option></select></label>
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
                <div v-else-if="videoTask?.status === 'completed' && videoTask?.url" class="creative-result-card">
                  <video :src="videoTask.url" controls playsinline class="w-full rounded-lg bg-black" />
                  <div class="mt-3 flex items-center justify-between gap-3">
                    <div class="text-xs text-gray-500 dark:text-gray-400">
                      {{ videoTask.model || '' }} · {{ videoTask.progress || '' }}
                    </div>
                    <a :href="videoTask.url" target="_blank" rel="noopener" class="btn btn-secondary px-3 py-1.5 text-xs">{{ t('creativeCenter.workspace.form.downloadVideo') }}</a>
                  </div>
                </div>
                <div v-else-if="videoTask" class="creative-result-card">
                  <div class="flex items-center gap-2 text-sm font-semibold text-violet-700 dark:text-violet-300">
                    <Icon :name="videoPolling ? 'refresh' : 'clock'" size="sm" :class="videoPolling ? 'animate-spin' : ''" />
                    {{ videoPolling ? t('creativeCenter.workspace.form.videoPolling') : t('creativeCenter.workspace.form.queued') }}
                  </div>
                  <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">{{ t('creativeCenter.workspace.form.taskId') }}：{{ videoTask.id || '—' }} · {{ videoTask.status || 'queued' }}</p>
                  <p v-if="videoTask.error?.message" class="mt-2 text-xs text-red-600 dark:text-red-400">{{ videoTask.error.message }}</p>
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
              <button type="button" class="btn btn-secondary flex-shrink-0" :disabled="historyLoading" @click="loadVideoHistory()">
                <Icon :name="historyLoading ? 'refresh' : 'refresh'" size="sm" :class="historyLoading ? 'animate-spin' : ''" />
                {{ t('common.refresh') }}
              </button>
            </div>

            <div v-if="historyError" class="creative-error-card">{{ historyError }}</div>

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
                  <video
                    v-if="item.url"
                    :src="item.url"
                    controls
                    playsinline
                    preload="none"
                    class="mt-3 w-full max-w-md rounded-lg bg-black"
                  />
                  <p class="mt-2 text-xs text-gray-400 dark:text-gray-500">{{ item.meta }}</p>
                </div>
                <a
                  v-if="item.url"
                  :href="item.url"
                  target="_blank"
                  rel="noopener"
                  class="btn btn-secondary flex-shrink-0"
                >
                  {{ item.action || t('creativeCenter.workspace.form.downloadVideo') }}
                  <Icon name="arrowRight" size="sm" />
                </a>
                <button v-else-if="item.action" type="button" class="btn btn-secondary flex-shrink-0" @click="openImageWorkspace">
                  {{ item.action }}
                  <Icon name="arrowRight" size="sm" />
                </button>
              </li>
              <li v-if="filteredHistoryItems.length === 0" class="p-10 text-center text-sm text-gray-500 dark:text-gray-400">
                {{ t('creativeCenter.sections.history.filters.empty') }}
              </li>
            </ul>
          </section>
      </div>
    </div>
  </AppLayout>
</template>

<style scoped>
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

.creative-provider-card {
  @apply rounded-xl border border-primary-200 bg-primary-50/70 p-3 dark:border-primary-900/50 dark:bg-primary-900/20;
}

.creative-key-onboarding {
  @apply rounded-xl border border-dashed border-primary-300 bg-primary-50/50 p-3 dark:border-primary-800 dark:bg-primary-900/10;
}

.creative-provider-status {
  @apply rounded-full px-2 py-0.5 text-[10px] font-semibold;
}

.creative-provider-status.is-ready {
  @apply bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300;
}

.creative-provider-status.is-fallback {
  @apply bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300;
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

.creative-result-card-error {
  @apply border-red-200 dark:border-red-900/50;
}

.creative-generated-images {
  @apply mt-4 flex flex-wrap justify-center gap-4;
}

.creative-generated-image {
  @apply relative w-full max-w-[720px] flex-1 overflow-hidden rounded-xl border border-gray-200 dark:border-dark-700;
  flex-basis: 360px;
}

.creative-generated-image-single {
  @apply mx-auto block;
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
