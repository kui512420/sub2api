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

export interface CreativeImageTask {
  id: string
  task_id?: string
  object?: string
  status: 'processing' | 'completed' | 'failed' | string
  poll_url?: string
  result?: CreativeImageGenerateResponse
  image_url?: string
  error?: { code?: string; message?: string } | null
  created_at?: number
  completed_at?: number | null
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
  generateCreativeImage,
  submitCreativeImageTask,
  getCreativeImageTask,
  createCreativeVideo,
  getCreativeVideo,
  downloadCreativeVideo,
}
