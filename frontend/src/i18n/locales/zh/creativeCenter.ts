export default {
  creativeCenter: {
    eyebrow: 'CREATIVE STUDIO',
    title: '创作中心',
    description: '在一个工作台中管理图片生成、视频生成和创作记录。',
    menuTitle: '创作工作台',
    menuHint: '选择一个创作模块开始',
    menu: {
      image: '图片生成',
      imageDescription: 'Prompt、参考图与批量任务',
      video: '视频生成',
      videoDescription: '异步任务与视频预览',
      history: '创作记录',
      historyDescription: '查看和管理历史任务'
    },
    tipTitle: '使用提示',
    tipDescription: '模型权限和生成费用由管理员配置的分组策略决定。',
    workspace: {
      parameters: {
        title: '参数',
        group: '服务分组',
        channel: '路由渠道',
        model: '模型',
        prompt: '提示词',
        size: '尺寸',
        quality: '质量',
        count: '生成数量',
        responseFormat: '返回格式',
        apiKey: 'API 密钥',
        duration: '视频时长',
        inputReference: '参考素材'
      },
      routing: {
        autoGroup: '自动选择',
        autoChannel: '自动路由',
        groupHint: '按当前账号可用权限匹配分组。',
        channelHint: '由网关根据分组和模型策略选择渠道。'
      },
      billing: {
        title: '费用预估',
        estimate: '提交后计算',
        byGroup: '按分组价格结算',
        description: '实际费用以网关返回的计费结果为准，生成前不会扣费。'
      },
      storage: {
        title: '结果存储',
        value: 'Cloudflare R2 对象存储',
        description: '生成文件上传 R2，页面只保存可控的结果地址和元数据。'
      },
      resultTitle: '结果',
      form: {
        groupPlaceholder: '选择服务分组',
        channelPlaceholder: '自动路由',
        modelPlaceholder: '选择模型',
        promptPlaceholder: '描述你想生成的内容……',
        referencePlaceholder: '粘贴图片 URL（可选）',
        uploadReference: '点击上传参考图片（可多选）',
        removeReference: '移除参考图片',
        referenceImageOnly: '请上传 PNG、JPG 或 WEBP 图片。',
        referenceImageTooLarge: '单张参考图不能超过 20 MB。',
        referenceImageLimit: '最多 4 张，单张不超过 20 MB',
        generateImage: '生成图片',
        generateVideo: '生成视频',
        generating: '提交中…',
        promptRequired: '请先输入提示词。',
        keyRequired: '请先在 API 密钥中创建一个可用密钥。',
        noKeyDescription: '首次使用？创建一个专用于椒图生图的密钥，创建后会自动选中。',
        createKey: '创建椒图生图密钥',
        keyCreating: '正在创建…',
        keyCreateFailed: '密钥创建失败，请稍后重试。',
        keyPlaceholder: '选择可用 API 密钥',
        balanceInsufficient: '账户余额不足，请先充值后再生成。',
        submitFailed: '提交失败，请稍后重试。',
        taskFailed: '图片生成失败',
        taskCancelled: '图片任务已取消',
        resultMissing: '任务已完成，但服务没有返回图片结果。',
        queued: '任务已提交，正在等待结果',
        videoPolling: '视频生成中，通常需要几十秒到几分钟…',
        downloadVideo: '下载视频',
        completed: '图片已生成',
        downloadImage: '下载图片',
        taskId: '任务编号'
      },
      image: {
        resultDescription: '通过椒图号池生成图片，结果会在这里预览并下载。',
        modelValue: '全能图片 V2（椒图号池）',
        modelHint: '页面优先展示椒图原生模型 ID；OpenAI 兼容客户端仍可使用 gpt-image-1。',
        promptValue: 'prompt / image（文生图或编辑）',
        sizeValue: '1024x1024 / 1536x1024',
        qualityValue: 'auto / standard / hd',
        countValue: 'n = 1–4',
        responseFormatValue: 'b64_json / url',
        emptyTitle: '暂时没有图片结果',
        emptyDescription: '提交标准 OpenAI 图片请求后，任务状态、预览和下载结果会显示在这里。',
        capabilities: {
          status: '任务状态追踪',
          preview: '在线预览结果',
          download: '下载 R2 文件'
        }
      },
      video: {
        resultDescription: '展示标准 OpenAI 视频接口的异步任务状态、预览和下载结果。',
        modelValue: 'OpenAI 视频模型（接入中）',
        modelHint: '模型名称直接对应标准 OpenAI 视频接口的 model 字段。',
        promptValue: 'prompt（文生视频或图生视频）',
        durationValue: '5–10 秒',
        sizeValue: '1280x720 / 720x1280',
        inputReferenceValue: '可选首帧或参考图',
        emptyTitle: '暂时没有视频结果',
        emptyDescription: '提交视频任务后，右侧会展示任务进度；完成后可预览并下载 R2 文件。'
      }
    },
    sections: {
      image: {
        eyebrow: 'IMAGE STUDIO',
        title: '图片生成',
        description: '创建图片任务、查看生成进度，并下载已完成的结果。',
        status: '已接入图片工作台'
      },
      video: {
        eyebrow: 'VIDEO STUDIO',
        title: '视频生成',
        description: '提交标准视频任务，跟踪异步进度并在结果完成后下载。',
        comingSoon: '已接入视频工作台',
        plannedTitle: '支持能力',
        plannedItems: {
          textToVideo: '文生视频与图生视频',
          asyncTasks: '异步任务队列和进度追踪',
          preview: '在线预览与结果下载'
        }
      },
      history: {
        eyebrow: 'CREATION HISTORY',
        title: '创作记录',
        description: '集中查看不同创作模块产生的任务和结果。',
        filters: {
          keyword: '搜索任务名称或提示词',
          allTypes: '全部类型',
          image: '图片任务',
          video: '视频任务',
          allStatuses: '全部状态',
          completed: '已完成',
          pending: '处理中',
          empty: '没有符合条件的创作记录'
        },
        imageTitle: '图片任务记录',
        imageDescription: '记录任务状态、结果、分组、渠道和实际费用。',
        videoTitle: '视频任务记录',
        videoDescription: '视频生成功能上线后，任务记录会显示在这里。',
        imageMeta: '分组：自动选择 · 渠道：自动路由 · 费用：提交后结算 · 存储：R2',
        videoMeta: '分组：自动选择 · 渠道：自动路由 · 费用：提交后结算 · 存储：R2',
        openImage: '查看图片记录',
        openVideo: '查看视频记录',
        videoProcessing: '视频生成中',
        videoFailed: '视频生成失败',
        loadFailed: '加载历史记录失败，请稍后重试。',
        pending: '等待视频模块上线'
      }
    }
  }
}
