import { apiClient } from '../client'

/** 对象存储中的一个生成产物（图片 / 视频）。 */
export interface StoredMediaObject {
  key: string
  kind: 'image' | 'video' | 'other'
  content_type: string
  size_bytes: number
  last_modified: string
  /** 后端现签的预签名预览 / 下载地址（私有桶，有时效）。 */
  url: string
}

export interface MediaLibrary {
  enabled: boolean
  bucket: string
  kind: string
  objects: StoredMediaObject[]
  next_token?: string
  is_truncated: boolean
}

export type MediaKindFilter = 'all' | 'image' | 'video'

export interface ListMediaParams {
  kind?: MediaKindFilter
  continuation_token?: string
  limit?: number
}

/** 列举对象存储里的生成产物（只覆盖图片 / 视频前缀）。 */
export async function listMediaObjects(params: ListMediaParams = {}): Promise<MediaLibrary> {
  const { data } = await apiClient.get<MediaLibrary>('/admin/backups/image-storage/objects', { params })
  return data
}

/** 删除单个生成产物（后端要求 step-up 2FA，调用方应用 useStepUp().run 包裹）。 */
export async function deleteMediaObject(key: string): Promise<void> {
  await apiClient.delete('/admin/backups/image-storage/objects', { params: { key } })
}

export const mediaAPI = {
  listMediaObjects,
  deleteMediaObject
}

export default mediaAPI
