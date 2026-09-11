<script setup lang="ts">
import { useRouter } from 'vue-router'

const router = useRouter()

const openImage = () => router.push('/batch-image')
</script>

<template>
  <div class="mx-auto max-w-6xl space-y-8 px-4 py-8">
    <div>
      <p class="text-sm font-medium text-emerald-600 dark:text-emerald-400">Creative Studio</p>
      <h1 class="mt-2 text-3xl font-bold text-gray-900 dark:text-white">创作中心</h1>
      <p class="mt-2 text-gray-500 dark:text-gray-400">统一管理图片和视频生成任务，选择模型后即可开始创作。</p>
    </div>

    <div class="grid gap-6 md:grid-cols-2">
      <button type="button" class="group rounded-2xl border border-gray-200 bg-white p-6 text-left shadow-sm transition hover:-translate-y-1 hover:border-emerald-400 hover:shadow-lg dark:border-dark-700 dark:bg-dark-800" @click="openImage">
        <div class="flex h-12 w-12 items-center justify-center rounded-xl bg-emerald-100 text-2xl dark:bg-emerald-900/30">🖼️</div>
        <h2 class="mt-5 text-xl font-semibold text-gray-900 dark:text-white">图片生成</h2>
        <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">支持 Prompt、参考图、尺寸和批量任务。进入现有图片工作台开始生成。</p>
        <span class="mt-5 inline-flex text-sm font-medium text-emerald-600 group-hover:underline dark:text-emerald-400">开始创作 →</span>
      </button>

      <div class="rounded-2xl border border-dashed border-gray-300 bg-gray-50 p-6 dark:border-dark-600 dark:bg-dark-800/50">
        <div class="flex h-12 w-12 items-center justify-center rounded-xl bg-violet-100 text-2xl dark:bg-violet-900/30">🎬</div>
        <h2 class="mt-5 text-xl font-semibold text-gray-900 dark:text-white">视频生成</h2>
        <p class="mt-2 text-sm leading-6 text-gray-500 dark:text-gray-400">视频生成页面正在接入异步任务、时长、分辨率和预览下载能力。</p>
        <span class="mt-5 inline-flex text-sm font-medium text-gray-400">即将上线</span>
      </div>
    </div>

    <div class="rounded-2xl bg-gradient-to-r from-emerald-50 to-sky-50 p-6 dark:from-emerald-900/20 dark:to-sky-900/20">
      <h3 class="font-semibold text-gray-900 dark:text-white">使用提示</h3>
      <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">可用模型和生成权限由管理员在分组设置中控制，生成费用会按实际结果计入用量。</p>
    </div>
  </div>
</template>
