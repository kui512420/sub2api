export default {
  creativeCenter: {
    eyebrow: 'CREATIVE STUDIO',
    title: 'Creative Center',
    description: 'Manage image generation, video generation, and creation history from one workspace.',
    menuTitle: 'Creative workspace',
    menuHint: 'Choose a module to get started',
    menu: {
      image: 'Image generation',
      imageDescription: 'Prompts, references, and batch jobs',
      video: 'Video generation',
      videoDescription: 'Async jobs and video previews',
      history: 'Creation history',
      historyDescription: 'Review and manage past jobs'
    },
    tipTitle: 'Usage tip',
    tipDescription: 'Model access and generation costs follow the group policies configured by the administrator.',
    workspace: {
      parameters: {
        title: 'Parameters',
        group: 'Service group',
        channel: 'Route channel',
        model: 'Model',
        prompt: 'Prompt',
        size: 'Size',
        quality: 'Quality',
        count: 'Count',
        responseFormat: 'Response format',
        apiKey: 'API key',
        duration: 'Duration',
        inputReference: 'Reference media'
      },
      routing: {
        autoGroup: 'Auto select',
        autoChannel: 'Auto route',
        groupHint: 'Match a group available to the current account.',
        channelHint: 'The gateway selects a channel from group and model policy.'
      },
      billing: {
        title: 'Cost estimate',
        estimate: 'Calculated on submit',
        byGroup: 'Settled by group pricing',
        description: 'The gateway response is the source of truth; no charge is made before generation.'
      },
      storage: {
        title: 'Result storage',
        value: 'Cloudflare R2 object storage',
        description: 'Generated files are uploaded to R2; the UI keeps controlled result URLs and metadata.'
      },
      resultTitle: 'Results',
      form: {
        groupPlaceholder: 'Select a service group',
        channelPlaceholder: 'Auto route',
        modelPlaceholder: 'Select a model',
        promptPlaceholder: 'Describe what you want to create…',
        referencePlaceholder: 'Paste an image URL (optional)',
        uploadReference: 'Click to upload reference images',
        removeReference: 'Remove reference image',
        referenceImageOnly: 'Please upload a PNG, JPG, or WEBP image.',
        referenceImageTooLarge: 'Each reference image must be 20 MB or smaller.',
        referenceImageLimit: 'Up to 4 images, 20 MB each',
        generateImage: 'Generate image',
        generateVideo: 'Generate video',
        generating: 'Submitting…',
        promptRequired: 'Enter a prompt first.',
        keyRequired: 'Create an active API key before generating.',
        noKeyDescription: 'First time here? Create a Jiaotu image key and it will be selected automatically.',
        createKey: 'Create Jiaotu image key',
        keyCreating: 'Creating…',
        keyCreateFailed: 'Could not create the key. Try again later.',
        keyPlaceholder: 'Select an active API key',
        balanceInsufficient: 'Your account balance is insufficient. Recharge before generating.',
        submitFailed: 'Submission failed. Try again later.',
        taskFailed: 'Image generation failed',
        taskCancelled: 'Image task was cancelled',
        resultMissing: 'The task completed, but the service returned no image result.',
        queued: 'Job submitted and waiting for results',
        videoPolling: 'Generating video, this usually takes tens of seconds to a few minutes…',
        downloadVideo: 'Download video',
        completed: 'Image generated',
        downloadImage: 'Download image',
        taskId: 'Task ID'
      },
      image: {
        resultDescription: 'Generate through the Jiaotu account pool, then preview and download the result here.',
        modelValue: 'All-purpose Image V2 (Jiaotu)',
        modelHint: 'The UI prefers Jiaotu provider model IDs; OpenAI-compatible clients can still use gpt-image-1.',
        promptValue: 'prompt / image (generation or edit)',
        sizeValue: '1024x1024 / 1536x1024',
        qualityValue: 'auto / standard / hd',
        countValue: 'n = 1–4',
        responseFormatValue: 'b64_json / url',
        emptyTitle: 'No image results yet',
        emptyDescription: 'After a standard OpenAI image request is submitted, task status, previews, and downloads will appear here.',
        capabilities: {
          status: 'Task status tracking',
          preview: 'In-browser previews',
          download: 'Download R2 files'
        }
      },
      video: {
        resultDescription: 'Display async status, previews, and downloads returned by the standard OpenAI video API.',
        modelValue: 'OpenAI video model (integrating)',
        modelHint: 'The model name maps directly to the standard OpenAI video API model field.',
        promptValue: 'prompt (text-to-video or image-to-video)',
        durationValue: '5–10 seconds',
        sizeValue: '1280x720 / 720x1280',
        inputReferenceValue: 'Optional first frame or reference image',
        emptyTitle: 'No video results yet',
        emptyDescription: 'After submission, progress appears here; completed jobs can preview and download their R2 file.'
      }
    },
    sections: {
      image: {
        eyebrow: 'IMAGE STUDIO',
        title: 'Image generation',
        description: 'Create image jobs, track progress, and download completed results.',
        status: 'Image workspace connected'
      },
      video: {
        eyebrow: 'VIDEO STUDIO',
        title: 'Video generation',
        description: 'Submit a standard video job, track async progress, and download the completed result.',
        comingSoon: 'Video workspace connected',
        plannedTitle: 'Capabilities',
        plannedItems: {
          textToVideo: 'Text-to-video and image-to-video',
          asyncTasks: 'Async job queue and progress tracking',
          preview: 'In-browser preview and downloads'
        }
      },
      history: {
        eyebrow: 'CREATION HISTORY',
        title: 'Creation history',
        description: 'Review jobs and results from each creative module in one place.',
        filters: {
          keyword: 'Search task name or prompt',
          allTypes: 'All types',
          image: 'Image tasks',
          video: 'Video tasks',
          allStatuses: 'All statuses',
          completed: 'Completed',
          pending: 'Processing',
          empty: 'No creation records match these filters'
        },
        imageTitle: 'Image jobs',
        imageDescription: 'Record status, results, group, channel, and actual cost.',
        videoTitle: 'Video jobs',
        videoDescription: 'Video job history will appear here when the module is available.',
        imageMeta: 'Group: Auto select · Channel: Auto route · Cost: settled on submit · Storage: R2',
        videoMeta: 'Group: Auto select · Channel: Auto route · Cost: settled on submit · Storage: R2',
        openImage: 'View image history',
        openVideo: 'View video history',
        videoProcessing: 'Generating video',
        videoFailed: 'Video generation failed',
        loadFailed: 'Could not load history. Try again later.',
        pending: 'Waiting for video module'
      }
    }
  }
}
