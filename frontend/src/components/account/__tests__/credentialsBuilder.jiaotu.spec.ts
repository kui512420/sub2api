import { describe, expect, it } from 'vitest'

import {
  JIAOTU_PLATFORM,
  buildJiaotuCredentials,
  readJiaotuAccountView
} from '../credentialsBuilder'

describe('buildJiaotuCredentials', () => {
  it('只写非空字段，并对 token 去掉 Bearer 前缀以外的空白', () => {
    const credentials = buildJiaotuCredentials({
      token: '  eyJhbGciOi.abc.def  ',
      phone: ' 13800001234 ',
      poolId: ' jp-9 ',
      nickName: '',
      userId: undefined,
      points: ' 68 '
    })

    expect(credentials).toEqual({
      token: 'eyJhbGciOi.abc.def',
      phone: '13800001234',
      jiaotu_id: 'jp-9',
      points: 68
    })
    // 空字符串字段一律省略，而不是写进去覆盖已有凭据
    expect(credentials).not.toHaveProperty('nick_name')
    expect(credentials).not.toHaveProperty('user_id')
  })

  it('积分非数字 / 负数时不写 points', () => {
    expect(buildJiaotuCredentials({ token: 't', points: 'abc' })).not.toHaveProperty('points')
    expect(buildJiaotuCredentials({ token: 't', points: -5 })).not.toHaveProperty('points')
    expect(buildJiaotuCredentials({ token: 't', points: '' })).not.toHaveProperty('points')
    expect(buildJiaotuCredentials({ token: 't', points: 12.9 }).points).toBe(12)
  })

  it('只填 token 时得到最小凭据；token 为空则完全不写 token 键', () => {
    expect(buildJiaotuCredentials({ token: 'tok' })).toEqual({ token: 'tok' })
    expect(buildJiaotuCredentials({ token: '   ' })).toEqual({})
  })

  it('平台常量与后端 service.PlatformJiaotu 口径一致', () => {
    expect(JIAOTU_PLATFORM).toBe('jiaotu')
  })
})

describe('readJiaotuAccountView', () => {
  it('读取展示字段，token 由 credentials_status.has_token 判定（后端已脱敏）', () => {
    const view = readJiaotuAccountView(
      { phone: '13800009999', jiaotu_id: 'jp-9', nick_name: '椒图081641', points: 49, jiaotu_status: 'OK' },
      { has_token: true }
    )
    expect(view).toEqual({
      phone: '13800009999',
      poolId: 'jp-9',
      nickName: '椒图081641',
      points: 49,
      status: 'ok',
      hasToken: true
    })
  })

  it('没有积分快照时 points 为 null，状态缺省为空串', () => {
    const view = readJiaotuAccountView({ points: '  ' }, null)
    expect(view.points).toBeNull()
    expect(view.status).toBe('')
    expect(view.hasToken).toBe(false)
  })

  it('credentials 完全缺失也不抛错（列表接口可能不带凭据）', () => {
    expect(readJiaotuAccountView(undefined, undefined)).toMatchObject({
      phone: '',
      poolId: '',
      points: null,
      status: '',
      hasToken: false
    })
  })
})
