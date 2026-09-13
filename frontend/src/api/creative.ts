import { buildGatewayUrl } from './client'

/** OpenAI-compatible image generation request. */
export interface CreativeImageGenerateRequest {
  model: string
  prompt: string
  n?: number
  size?: string
  quality?: string
  response_format?: 'url' | 'b64_json' | string
  style?: string
  user?: string
  [key: string]: unknown
}

export interface CreativeImageData {
  url?: string
  b64_json?: string
  revised_prompt?: string
  [key: string]: unknown
}

export interface CreativeImageGenerateResponse {
  created?: number
  data: CreativeImageData[]
  [key: string]: unknown
}

export interface CreativeModel {
  id: string
  name?: string
  display_name?: string
  capability?: string
  type?: string
  provider?: string
  provider_model_name?: string
  provider_model_id?: number
  aliases?: string[]
  alias_for?: string
  [key: string]: unknown
}

export interface CreativeImageTask {
  id: string
  task_id?: string
  object?: string
  status: 'processing' | 'completed' | 'failed' | 'cancelled' | string
  poll_url?: string
  result?: CreativeImageGenerateResponse
  image_url?: string
  error?: { code?: string; type?: string; message?: string } | null
  http_status?: number
  created_at?: number
  completed_at?: number | null
  expires_at?: number
  [key: string]: unknown
}

/** OpenAI-compatible asynchronous video creation request. */
export interface CreativeVideoCreateRequest {
  model: string
  prompt: string
  seconds?: number | string
  size?: string
  input_reference?: string | string[]
  [key: string]: unknown
}

export interface CreativeVideoJob {
  id: string
  object?: string
  status?: string
  progress?: number
  model?: string
  created_at?: number
  completed_at?: number | null
  error?: { code?: string; message?: string } | null
  [key: string]: unknown
}

async function parseCreativeError(response: Response): Promise<Error> {
  let message = response.statusText || `HTTP ${response.status}`
  let body: Record<string, unknown> | null = null
  try {
    body = (await response.json()) as Record<string, unknown>
    const error = body?.error
    if (error && typeof error === 'object') {
      message = String((error as Record<string, unknown>).message || message)
    } else if (body?.message) {
      message = String(body.message)
    }
  } catch {
    // Non-JSON gateway errors keep the HTTP status text above.
  }
  const result = new Error(message)
  ;(result as Error & { status?: number; code?: unknown; requestId?: string }).status = response.status
  ;(result as Error & { status?: number; code?: unknown; requestId?: string }).code =
    body && typeof body.error === 'object' && body.error !== null
      ? (body.error as Record<string, unknown>).code
      : undefined
  ;(result as Error & { status?: number; code?: unknown; requestId?: string }).requestId =
    response.headers.get('X-Request-Id') || ''
  return result
}

function authHeaders(apiKey: string, extra?: HeadersInit): HeadersInit {
  return {
    Authorization: `Bearer ${apiKey}`,
    ...extra,
  }
}

/**
 * 按 capability 拉创作中心可选模型。
 *
 * 不能只靠 `id.startsWith('jiaotu-')` 判图片：椒图的图片与视频稳定 ID 共用同一前缀
 * （`jiaotu-image-v2` vs `jiaotu-minimax-h3`），那样会把视频模型混进图片下拉框，
 * 提交后被子路由拒为 400。有 capability / type 标注时一律以它为准，
 * 只对不返该字段的旧网关回退到 id 前缀猜测。
 */
export async function listCreativeModels(
  apiKey: string,
  options: { signal?: AbortSignal; capability?: 'image' | 'video' } = {}
): Promise<CreativeModel[]> {
  const want = options.capability || 'image'
  const response = await fetch(buildGatewayUrl('/v1/models'), {
    method: 'GET',
    headers: authHeaders(apiKey),
    signal: options.signal,
  })
  if (!response.ok) throw await parseCreativeError(response)
  const body = await response.json() as { data?: CreativeModel[] }
  if (!Array.isArray(body.data)) return []
  return body.data.filter((model) => {
    if (!model || typeof model.id !== 'string') return false
    const declared = (typeof model.capability === 'string' ? model.capability : '').toLowerCase()
      || (typeof model.type === 'string' ? model.type : '').toLowerCase()
    if (declared === 'image' || declared === 'video') return declared === want
    if (want !== 'image') return false
    return model.id.startsWith('gpt-image-') || model.id.startsWith('jiaotu-')
  })
}

/** 拉当前路由可用的视频模型（入站 /v1/videos）。 */
export function listCreativeVideoModels(
  apiKey: string,
  options: { signal?: AbortSignal } = {}
): Promise<CreativeModel[]> {
  return listCreativeModels(apiKey, { ...options, capability: 'video' })
}

async function gatewayJSON<T>(apiKey: string, path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(buildGatewayUrl(path), {
    ...init,
    headers: authHeaders(apiKey, {
      'Content-Type': 'application/json',
      ...(init.headers || {}),
    }),
  })
  if (!response.ok) throw await parseCreativeError(response)
  return response.json() as Promise<T>
}

async function gatewayMultipart<T>(apiKey: string, path: string, formData: FormData, options: { signal?: AbortSignal; idempotencyKey?: string } = {}): Promise<T> {
  const headers: Record<string, string> = {}
  if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey
  const response = await fetch(buildGatewayUrl(path), {
    method: 'POST',
    headers: authHeaders(apiKey, headers),
    body: formData,
    signal: options.signal,
  })
  if (!response.ok) throw await parseCreativeError(response)
  return response.json() as Promise<T>
}

/** Submit one OpenAI image generation request through the configured gateway. */
export function generateCreativeImage(
  apiKey: string,
  payload: CreativeImageGenerateRequest,
  options: { idempotencyKey?: string; signal?: AbortSignal; endpoint?: string } = {},
): Promise<CreativeImageGenerateResponse> {
  const headers: Record<string, string> = {}
  if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey
  return gatewayJSON<CreativeImageGenerateResponse>(apiKey, options.endpoint || '/v1/images/generations', {
    method: 'POST',
    headers,
    body: JSON.stringify(payload),
    signal: options.signal,
  })
}

/** Submit an image edit request with one or more reference image files. */
export function editCreativeImage(
  apiKey: string,
  payload: CreativeImageGenerateRequest,
  files: File[],
  options: { idempotencyKey?: string; signal?: AbortSignal; endpoint?: string } = {},
): Promise<CreativeImageGenerateResponse> {
  const formData = new FormData()
  formData.append('model', payload.model)
  formData.append('prompt', payload.prompt)
  if (payload.n != null) formData.append('n', String(payload.n))
  if (payload.size) formData.append('size', String(payload.size))
  if (payload.quality) formData.append('quality', String(payload.quality))
  if (payload.response_format) formData.append('response_format', String(payload.response_format))
  if (payload.style) formData.append('style', String(payload.style))
  files.forEach((file, index) => {
    formData.append(files.length === 1 ? 'image' : `image[${index}]`, file, file.name)
  })
  return gatewayMultipart<CreativeImageGenerateResponse>(apiKey, options.endpoint || '/v1/images/edits', formData, options)
}

/**
 * Submit an asynchronous image task. When R2 image storage is enabled this is
 * the preferred user-facing path: the server uploads each output and exposes
 * only the compact result URLs from the task poll response.
 */
export function submitCreativeImageTask(
  apiKey: string,
  payload: CreativeImageGenerateRequest,
  options: { idempotencyKey?: string; signal?: AbortSignal; endpoint?: string } = {},
): Promise<CreativeImageTask> {
  const headers: Record<string, string> = {}
  if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey
  return gatewayJSON<CreativeImageTask>(apiKey, options.endpoint || '/v1/images/generations/async', {
    method: 'POST',
    headers,
    body: JSON.stringify(payload),
    signal: options.signal,
  })
}

export function getCreativeImageTask(
  apiKey: string,
  taskId: string,
  options: { endpoint?: string; signal?: AbortSignal } = {},
): Promise<CreativeImageTask> {
  const base = options.endpoint || '/v1/images/tasks'
  return gatewayJSON<CreativeImageTask>(apiKey, `${base}/${encodeURIComponent(taskId)}`, {
    method: 'GET',
    signal: options.signal,
  })
}

/** Submit an asynchronous video job. The endpoint is configurable for providers. */
export function createCreativeVideo(
  apiKey: string,
  payload: CreativeVideoCreateRequest,
  options: { idempotencyKey?: string; signal?: AbortSignal; endpoint?: string } = {},
): Promise<CreativeVideoJob> {
  const headers: Record<string, string> = {}
  if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey
  return gatewayJSON<CreativeVideoJob>(apiKey, options.endpoint || '/v1/videos/generations', {
    method: 'POST',
    headers,
    body: JSON.stringify(payload),
    signal: options.signal,
  })
}

/** Poll a video job using its provider-neutral id. */
export function getCreativeVideo(
  apiKey: string,
  videoId: string,
  options: { endpoint?: string; signal?: AbortSignal } = {},
): Promise<CreativeVideoJob> {
  const base = options.endpoint || '/v1/videos/generations'
  return gatewayJSON<CreativeVideoJob>(apiKey, `${base}/${encodeURIComponent(videoId)}`, {
    method: 'GET',
    signal: options.signal,
  })
}

/** List this key's video jobs (creation-history page). Newest first, capped server-side. */
export function listCreativeVideos(
  apiKey: string,
  options: { limit?: number; endpoint?: string; signal?: AbortSignal } = {},
): Promise<CreativeVideoJob[]> {
  const base = options.endpoint || '/v1/videos'
  const query = options.limit ? `?limit=${encodeURIComponent(String(options.limit))}` : ''
  return gatewayJSON<{ data?: CreativeVideoJob[] }>(apiKey, `${base}${query}`, {
    method: 'GET',
    signal: options.signal,
  }).then((body) => (Array.isArray(body?.data) ? body.data : []))
}

/** Download the completed video content; the server may proxy or sign its R2 object URL. */
export async function downloadCreativeVideo(
  apiKey: string,
  videoId: string,
  options: { endpoint?: string; signal?: AbortSignal } = {},
): Promise<Blob> {
  const base = options.endpoint || '/v1/videos/generations'
  const response = await fetch(buildGatewayUrl(`${base}/${encodeURIComponent(videoId)}/content`), {
    method: 'GET',
    headers: authHeaders(apiKey),
    signal: options.signal,
  })
  if (!response.ok) throw await parseCreativeError(response)
  return response.blob()
}

export default {
  listCreativeModels,
  generateCreativeImage,
  editCreativeImage,
  submitCreativeImageTask,
  getCreativeImageTask,
  createCreativeVideo,
  getCreativeVideo,
  listCreativeVideos,
  downloadCreativeVideo,
}
