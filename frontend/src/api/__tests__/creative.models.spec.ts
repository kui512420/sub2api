import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { listCreativeModels, listCreativeVideoModels } from '../creative'

// /v1/models 的 capability 划分是创作中心两个下拉框的唯一数据来源。
// 椒图的图片与视频稳定 ID 共用 `jiaotu-` 前缀，所以这里重点锁住：
// 标注了 capability 的条目必须按 capability 分家，不能靠前缀猜。
const catalogue = [
  { id: 'jiaotu-image-v2', capability: 'image', provider: 'jiaotu' },
  { id: 'jiaotu-seedream-5-pro', capability: 'image', provider: 'jiaotu' },
  { id: 'jiaotu-minimax-h3', capability: 'video', provider: 'jiaotu' },
  { id: 'jiaotu-seedance-2', capability: 'video', provider: 'jiaotu' },
  { id: 'gpt-image-1', capability: 'image', provider: 'openai' },
  { id: 'sora-1', type: 'video', provider: 'openai' },
  // 旧网关条目：没有任何 capability / type 标注
  { id: 'jiaotu-legacy-image', provider: 'jiaotu' },
  { id: 'claude-3-5-sonnet', provider: 'anthropic' },
]

function stubModelsResponse(items: unknown[]) {
  const fetchMock = vi.fn(async () => ({
    ok: true,
    status: 200,
    headers: new Headers({ 'Content-Type': 'application/json' }),
    json: async () => ({ data: items }),
  }))
  vi.stubGlobal('fetch', fetchMock)
  return fetchMock
}

describe('creative model catalogue filtering', () => {
  beforeEach(() => {
    vi.unstubAllGlobals()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('图片列表只留 image 条目，椒图视频模型不得混入', async () => {
    stubModelsResponse(catalogue)
    const models = await listCreativeModels('sk-test')
    expect(models.map((model) => model.id)).toEqual([
      'jiaotu-image-v2',
      'jiaotu-seedream-5-pro',
      'gpt-image-1',
      // 无标注时按前缀回退：jiaotu-legacy-image 当图片入口，claude 不是
      'jiaotu-legacy-image',
    ])
  })

  it('视频列表按 capability / type 命中，且不吃前缀回退', async () => {
    stubModelsResponse(catalogue)
    const models = await listCreativeVideoModels('sk-test')
    expect(models.map((model) => model.id)).toEqual([
      'jiaotu-minimax-h3',
      'jiaotu-seedance-2',
      'sora-1',
    ])
  })

  it('两个列表互斥：同一模型不会同时出现在图片与视频下拉框里', async () => {
    stubModelsResponse(catalogue)
    const [images, videos] = await Promise.all([
      listCreativeModels('sk-test'),
      listCreativeVideoModels('sk-test'),
    ])
    const imageIds = new Set(images.map((model) => model.id))
    for (const video of videos) {
      expect(imageIds.has(video.id)).toBe(false)
    }
  })
})
