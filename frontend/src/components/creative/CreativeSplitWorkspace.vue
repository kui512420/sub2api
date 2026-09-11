<script setup lang="ts">
import { computed } from 'vue'
import Icon from '@/components/icons/Icon.vue'

export interface CreativeWorkspaceParameter {
  label: string
  value: string
  hint?: string
}

const props = withDefaults(defineProps<{
  eyebrow: string
  title: string
  description: string
  resultTitle: string
  resultDescription: string
  parametersTitle: string
  parameters: CreativeWorkspaceParameter[]
  protocolEndpoint?: string
  billingTitle?: string
  billingEstimate?: string
  billingDescription?: string
  storageTitle?: string
  storageValue?: string
  storageDescription?: string
  status?: string
  actionLabel?: string
  actionHint?: string
  actionDisabled?: boolean
  submitting?: boolean
  accent?: 'primary' | 'violet'
}>(), {
  status: '',
  protocolEndpoint: '',
  billingTitle: '',
  billingEstimate: '',
  billingDescription: '',
  storageTitle: '',
  storageValue: '',
  storageDescription: '',
  actionLabel: '',
  actionHint: '',
  actionDisabled: false,
  submitting: false,
  accent: 'primary',
})

const emit = defineEmits<{
  action: []
}>()

const accentClasses = computed(() => {
  if (props.accent === 'violet') {
    return {
      icon: 'bg-violet-100 text-violet-600 dark:bg-violet-900/30 dark:text-violet-300',
      eyebrow: 'text-violet-600 dark:text-violet-300',
      status: 'bg-violet-50 text-violet-700 dark:bg-violet-900/30 dark:text-violet-300',
      action: 'btn btn-secondary border-violet-200 text-violet-700 hover:border-violet-300 dark:border-violet-800 dark:text-violet-300',
    }
  }
  return {
    icon: 'bg-primary-100 text-primary-600 dark:bg-primary-900/30 dark:text-primary-300',
    eyebrow: 'text-primary-600 dark:text-primary-300',
    status: 'bg-emerald-50 text-emerald-700 dark:bg-emerald-900/30 dark:text-emerald-300',
    action: 'btn btn-primary',
  }
})
</script>

<template>
  <div class="creative-split-workspace">
    <aside class="creative-parameter-panel">
      <div class="creative-panel-heading">
        <div class="flex items-center gap-3">
          <div class="creative-panel-icon" :class="accentClasses.icon">
            <Icon name="cog" size="md" />
          </div>
          <div class="min-w-0">
            <h2 class="text-xl font-bold text-gray-900 dark:text-white">{{ title }}</h2>
          </div>
        </div>
      </div>

      <div class="creative-parameter-body">
        <p class="text-xs font-semibold uppercase tracking-[0.14em] text-gray-500 dark:text-gray-400">{{ parametersTitle }}</p>
        <slot v-if="$slots.parameters" name="parameters" />
        <dl v-else class="mt-3 space-y-2.5">
          <div v-for="parameter in parameters" :key="parameter.label" class="creative-parameter-row">
            <dt class="text-xs text-gray-500 dark:text-gray-400">{{ parameter.label }}</dt>
            <dd class="mt-1 text-sm font-medium text-gray-800 dark:text-gray-100">{{ parameter.value }}</dd>
            <p v-if="parameter.hint" class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ parameter.hint }}</p>
          </div>
        </dl>

        <div v-if="billingTitle" class="creative-billing-card">
          <div class="flex items-start justify-between gap-3">
            <div>
              <p class="text-xs font-semibold uppercase tracking-[0.12em] text-gray-500 dark:text-gray-400">{{ billingTitle }}</p>
              <p class="mt-2 text-lg font-bold text-gray-900 dark:text-white">{{ billingEstimate }}</p>
            </div>
            <Icon name="dollar" size="md" class="text-emerald-600 dark:text-emerald-300" />
          </div>
          <p v-if="billingDescription" class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ billingDescription }}</p>
        </div>

      </div>

      <div v-if="actionLabel || $slots.action" class="mt-auto border-t border-gray-200 p-4 dark:border-dark-700">
        <p v-if="actionHint" class="mb-3 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ actionHint }}</p>
        <slot name="action">
          <button type="button" class="w-full justify-center" :class="accentClasses.action" :disabled="actionDisabled || submitting" @click="emit('action')">
            <Icon v-if="submitting" name="refresh" size="sm" class="animate-spin" />
            <span>{{ actionLabel }}</span>
            <Icon v-if="!submitting" name="arrowRight" size="sm" />
          </button>
        </slot>
      </div>
    </aside>

    <section class="creative-result-panel">
      <header class="creative-result-header">
        <h3 class="text-base font-semibold text-gray-900 dark:text-white">{{ resultTitle }}</h3>
      </header>
      <div class="creative-result-body">
        <slot name="results" />
      </div>
    </section>
  </div>
</template>

<style scoped>
.creative-split-workspace {
  @apply grid min-h-0 w-full min-w-0 flex-1 overflow-hidden rounded-2xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-800;
  grid-template-columns: minmax(220px, 280px) minmax(0, 1fr);
}

.creative-parameter-panel {
  @apply flex min-h-0 flex-col border-r border-gray-200 bg-gray-50/80 dark:border-dark-700 dark:bg-dark-900/40;
}

.creative-panel-heading {
  @apply border-b border-gray-200 p-5 dark:border-dark-700;
}

.creative-panel-icon {
  @apply flex h-10 w-10 flex-shrink-0 items-center justify-center rounded-xl;
}

.creative-parameter-body {
  @apply min-h-0 flex-1 overflow-y-auto p-4;
}

.creative-parameter-row {
  @apply rounded-xl border border-gray-200 bg-white p-3 dark:border-dark-700 dark:bg-dark-800;
}

.creative-billing-card {
  @apply mt-4 rounded-xl border border-emerald-200 bg-emerald-50/70 p-3 dark:border-emerald-900/50 dark:bg-emerald-900/20;
}

.creative-result-panel {
  @apply flex min-h-0 min-w-0 flex-col bg-gray-50/50 dark:bg-dark-950/20;
}

.creative-result-header {
  @apply flex flex-shrink-0 flex-col gap-3 border-b border-gray-200 px-5 py-4 sm:flex-row sm:items-center sm:justify-between dark:border-dark-700;
}

.creative-status-pill {
  @apply inline-flex w-fit items-center gap-2 rounded-full px-3 py-1.5 text-xs font-medium;
}

.creative-result-body {
  @apply flex min-h-0 flex-1 flex-col overflow-hidden p-4;
}

@media (max-width: 900px) {
  .creative-split-workspace {
    display: flex;
    flex-direction: column;
    overflow: visible;
  }

  .creative-parameter-panel {
    border-right: 0;
    border-bottom: 1px solid rgb(229 231 235);
  }

  .dark .creative-parameter-panel {
    border-bottom-color: rgb(55 65 81);
  }

  .creative-parameter-body {
    flex: none;
  }

  .creative-result-panel {
    min-height: 520px;
  }
}
</style>
