import { defineComponent } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { importPoolMock, maintenanceMock, showSuccessMock, showErrorMock } = vi.hoisted(() => ({
  importPoolMock: vi.fn(),
  maintenanceMock: vi.fn(),
  showSuccessMock: vi.fn(),
  showErrorMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: showErrorMock,
    showSuccess: showSuccessMock
  })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      importJiaotuPool: importPoolMock,
      jiaotuPoolMaintenance: maintenanceMock
    }
  }
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

import JiaotuPoolImportModal from '@/components/admin/account/JiaotuPoolImportModal.vue'
import JiaotuPointsCell from '@/components/account/JiaotuPointsCell.vue'
import type { Account } from '@/types'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

const groups = [
  { id: 3, name: '椒图图片组', platform: 'jiaotu' },
  { id: 7, name: 'OpenAI 组', platform: 'openai' }
] as never

function mountModal() {
  return mount(JiaotuPoolImportModal, {
    props: { show: true, groups },
    global: { stubs: { BaseDialog: BaseDialogStub } }
  })
}

describe('JiaotuPoolImportModal', () => {
  beforeEach(() => {
    importPoolMock.mockReset()
    maintenanceMock.mockReset()
    showSuccessMock.mockReset()
    showErrorMock.mockReset()
  })

  it('只把 platform=jiaotu 的分组列进绑定下拉', async () => {
    const wrapper = mountModal()
    const options = wrapper.get('[data-testid="jiaotu-pool-group"]').findAll('option')
    expect(options.map((option) => option.text())).toEqual([
      'admin.accounts.jiaotu.poolImport.noGroupSelected',
      '椒图图片组'
    ])
  })

  it('粘贴号池后提交：content 原文下发，并回报导入计数与失败明细', async () => {
    importPoolMock.mockResolvedValue({
      total: 2,
      created: 1,
      updated: 0,
      skipped: 1,
      failed: 0,
      ready: 1,
      unusable: 1,
      target_size: 2,
      errors: [],
      warnings: [{ index: 1, name: 'jp-10', message: '账号已导入但下架失败：db down' }]
    })

    const wrapper = mountModal()
    await wrapper.get('[data-testid="jiaotu-pool-content"]').setValue('{"accounts":[{"id":"jp-9","token":"t"}]}')
    await wrapper.get('[data-testid="jiaotu-pool-group"]').setValue(3)
    await wrapper.get('[data-testid="jiaotu-pool-name-prefix"]').setValue('椒图')
    await wrapper.get('#jiaotu-pool-import-form').trigger('submit.prevent')
    await flushPromises()

    expect(importPoolMock).toHaveBeenCalledTimes(1)
    const payload = importPoolMock.mock.calls[0]?.[0]
    expect(payload.content).toBe('{"accounts":[{"id":"jp-9","token":"t"}]}')
    expect(payload.group_ids).toEqual([3])
    expect(payload.name_prefix).toBe('椒图')
    expect(payload.concurrency).toBe(1)
    expect(payload.update_existing).toBe(false)

    expect(showSuccessMock).toHaveBeenCalledWith(
      expect.stringContaining('admin.accounts.jiaotu.poolImport.success')
    )
    expect(wrapper.text()).toContain('账号已导入但下架失败：db down')
    expect(wrapper.emitted('imported')).toBeTruthy()
  })

  it('content 为空时前端拦下，不打后端', async () => {
    const wrapper = mountModal()
    await wrapper.get('#jiaotu-pool-import-form').trigger('submit.prevent')
    await flushPromises()

    expect(importPoolMock).not.toHaveBeenCalled()
    expect(showErrorMock).toHaveBeenCalledWith('admin.accounts.jiaotu.poolImport.emptyContent')
  })

  it('维护走免费接口并把并发/上限/签到透传', async () => {
    maintenanceMock.mockResolvedValue({
      total: 61,
      refreshed: 58,
      unchanged: 30,
      failed: 2,
      expired: 1,
      sign_in: { attempted: 61, success: 40, already: 20, failed: 1 }
    })

    const wrapper = mountModal()
    await wrapper.get('[data-testid="jiaotu-pool-maintenance-run"]').trigger('click')
    await flushPromises()

    const payload = maintenanceMock.mock.calls[0]?.[0]
    expect(payload).toEqual({
      refresh_points: true,
      sign_in: false,
      concurrency: 5,
      limit: 0
    })
    expect(wrapper.text()).toContain('admin.accounts.jiaotu.maintenance.signInSummary')
  })
})

describe('JiaotuPointsCell', () => {
  const accountWith = (credentials: Record<string, unknown>, status?: Record<string, boolean>): Account =>
    ({
      id: 1,
      name: 'jp-9',
      platform: 'jiaotu',
      type: 'apikey',
      credentials,
      credentials_status: status
    }) as unknown as Account

  it('渲染积分快照 + 正常态徽标 + 号池 ID/手机号', () => {
    const wrapper = mount(JiaotuPointsCell, {
      props: {
        account: accountWith(
          { points: 49, jiaotu_status: 'ok', jiaotu_id: 'jp-9', phone: '13800009999' },
          { has_token: true }
        )
      }
    })

    expect(wrapper.get('[data-testid="jiaotu-points-value"]').text()).toContain('49')
    expect(wrapper.get('[data-testid="jiaotu-points-status"]').text()).toBe('admin.accounts.jiaotu.statusOk')
    expect(wrapper.text()).toContain('jp-9 · 13800009999')
  })

  it('无快照显示占位文案，expired 显示失效徽标', () => {
    const empty = mount(JiaotuPointsCell, { props: { account: accountWith({}) } })
    expect(empty.get('[data-testid="jiaotu-points-value"]').text()).toContain(
      'admin.accounts.jiaotu.noPointsSnapshot'
    )
    expect(empty.find('[data-testid="jiaotu-points-status"]').exists()).toBe(false)

    const expired = mount(JiaotuPointsCell, {
      props: { account: accountWith({ points: 0, jiaotu_status: 'EXPIRED' }) }
    })
    expect(expired.get('[data-testid="jiaotu-points-status"]').text()).toBe(
      'admin.accounts.jiaotu.statusExpired'
    )
  })

  it('token 已脱敏时也只用 credentials_status 判断存在性', () => {
    const wrapper = mount(JiaotuPointsCell, {
      props: { account: accountWith({ points: 12 }, { has_token: true }) }
    })
    expect(wrapper.text()).not.toContain('undefined')
    expect(wrapper.get('[data-testid="jiaotu-points-value"]').text()).toContain('12')
  })
})
