export default {
  media: {
    title: '素材库',
    description: '预览与管理创作中心生成并转存到对象存储（R2/S3）的图片和视频。',
    bucket: '存储桶',
    total: '共 {count} 项',
    tabs: {
      all: '全部',
      image: '图片',
      video: '视频'
    },
    refresh: '刷新',
    loading: '加载中…',
    loadMore: '加载更多',
    loadingMore: '加载中…',
    empty: '该分类下暂无生成产物',
    fields: {
      kind: '类型',
      size: '大小',
      modifiedAt: '生成时间',
      key: '对象键'
    },
    kind: {
      image: '图片',
      video: '视频',
      other: '其他'
    },
    actions: {
      open: '新窗口打开',
      download: '下载',
      delete: '删除'
    },
    preview: {
      title: '预览'
    },
    deleteConfirm: '确定删除该对象吗？此操作会同时删除对象存储中的文件，且不可恢复。',
    deleted: '已删除',
    deleteFailed: '删除失败',
    loadFailed: '加载素材库失败',
    unavailable: {
      title: '对象存储未启用或未配置完整',
      description: '素材库依赖已启用且凭证完整的对象存储。请先在「备份与存储」中配置并开启异步生图对象存储。',
      goConfigure: '前往配置'
    }
  }
}
