<script setup lang="ts">
import { computed } from "vue";
import { Clock3, FolderOpen, Play, Save, Settings, Trash2, Cog } from "lucide-vue-next";
import { appStore } from "../stores/appStore";
import CodeEditor from "./CodeEditor.vue";

const props = defineProps<{
  isRunning: boolean;
}>();

const emit = defineEmits<{
  deleteCurrent: [];
  openConfig: [];
}>();

const { state } = appStore;

const selectedFileName = computed(() => {
  if (!state.selectedScriptPath) {
    return "";
  }
  const parts = state.selectedScriptPath.split("/");
  return parts[parts.length - 1] || state.selectedScriptPath;
});

const createdAtText = computed(() => {
  if (!state.selectedScriptPath) {
    return "";
  }

  const findNode = (nodes: typeof state.treeNodes, target: string): (typeof state.treeNodes)[number] | undefined => {
    for (const node of nodes) {
      if (node.relativePath === target) {
        return node;
      }
      if (node.kind === "folder" && node.children?.length) {
        const found = findNode(node.children, target);
        if (found) {
          return found;
        }
      }
    }
    return undefined;
  };

  const node = findNode(state.treeNodes, state.selectedScriptPath);
  if (!node?.createdAtMs) {
    return "";
  }
  return formatDateTime(node.createdAtMs);
});

const runtimeDisplayText = computed(() => {
  if (props.isRunning) {
    return "运行中";
  }
  if (!state.selectedScriptPath || !state.runtimeRecords[state.selectedScriptPath]) {
    return "尚未运行";
  }
  const runtime = state.runtimeRecords[state.selectedScriptPath];
  const status = runtime.lastStatus === "success" ? "成功" : "失败";
  return `${status} ${formatDateTime(runtime.lastRunAt)}`;
});

const runtimeTagClass = computed(() => {
  if (props.isRunning) {
    return "bg-blue-500/12 text-blue-700";
  }
  if (!state.selectedScriptPath || !state.runtimeRecords[state.selectedScriptPath]) {
    return "bg-slate-500/12 text-slate-700";
  }
  return state.runtimeRecords[state.selectedScriptPath].lastStatus === "success"
    ? "bg-emerald-500/14 text-emerald-700"
    : "bg-red-500/14 text-red-700";
});

const runtimeDotClass = computed(() => {
  if (props.isRunning) {
    return "bg-blue-600";
  }
  if (!state.selectedScriptPath || !state.runtimeRecords[state.selectedScriptPath]) {
    return "bg-slate-500";
  }
  return state.runtimeRecords[state.selectedScriptPath].lastStatus === "success" ? "bg-emerald-600" : "bg-red-600";
});

const interpreterText = computed(() => {
  return state.selectedScriptConfig.interpreterDisplay || "未配置解释器";
});
const showInterpreterTag = computed(() => !!state.selectedScriptConfig.interpreterPath);

function formatDateTime(ts: number) {
  const date = new Date(ts);
  const pad = (v: number) => String(v).padStart(2, "0");
  return `${date.getFullYear()}/${pad(date.getMonth() + 1)}/${pad(date.getDate())} ${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

function openLocation() {
  if (!state.selectedScriptPath) {
    return;
  }
  appStore.openPath(state.selectedScriptPath);
}
</script>

<template>
  <div v-if="!state.selectedScriptPath" class="h-full flex items-center justify-center text-slate-600 text-sm">
    请选择左侧脚本开始编辑
  </div>

  <div v-else class="h-full flex flex-col">
    <div class="px-4 py-3 border-b border-white/55 bg-white/26 backdrop-blur-xl">
      <div class="flex items-center justify-between gap-3">
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-slate-900 truncate">{{ selectedFileName }}</h2>
          <p class="text-xs text-slate-600 mt-1 truncate">{{ state.selectedScriptPath }}</p>
        </div>
        <button
          class="btn-solid btn-primary px-3 py-1.5 text-sm flex items-center gap-2"
          :disabled="isRunning"
          @click="appStore.runCurrentScript()"
        >
          <Play class="w-4 h-4" />
          {{ isRunning ? "运行中" : "运行" }}
        </button>
      </div>

      <div class="mt-2 text-xs text-slate-700 flex items-center gap-2 flex-wrap">
        <span class="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-slate-500/12">
          <Clock3 class="w-3.5 h-3.5 text-slate-600" />
          创建于 {{ createdAtText || "-" }}
        </span>
        <span
          class="inline-flex items-center gap-1.5 px-2 py-1 rounded-md"
          :class="runtimeTagClass"
        >
          <span class="w-2 h-2 rounded-full" :class="runtimeDotClass" />
          {{ runtimeDisplayText }}
        </span>
        <span v-if="showInterpreterTag" class="inline-flex items-center gap-1.5 px-2 py-1 rounded-md bg-blue-500/12 text-blue-700">
          <Cog class="w-3.5 h-3.5" />
          {{ interpreterText }}
        </span>
      </div>
    </div>

    <div class="flex-1 p-3 overflow-hidden">
      <div class="h-full rounded-xl overflow-hidden border border-white/70 arc-editor-stage">
        <CodeEditor
          :model-value="state.selectedScriptContent"
          :file-name="selectedFileName"
          @update:model-value="appStore.updateEditorContent"
        />
      </div>
    </div>

    <div class="px-4 py-3 border-t border-white/55 bg-white/26 backdrop-blur-xl flex items-center justify-between gap-2 flex-wrap">
      <div class="flex items-center gap-2 flex-wrap">
        <button
          class="btn-solid btn-neutral px-3 py-1.5 text-sm flex items-center gap-1.5"
          @click="openLocation"
        >
          <FolderOpen class="w-4 h-4" />
          打开位置
        </button>
        <button
          class="btn-solid btn-neutral px-3 py-1.5 text-sm flex items-center gap-1.5"
          @click="emit('openConfig')"
        >
          <Settings class="w-4 h-4" />
          脚本配置
        </button>
      </div>

      <div class="flex items-center gap-2">
        <button
          class="btn-solid btn-info px-3 py-1.5 text-sm flex items-center gap-1.5"
          :disabled="!state.dirty"
          @click="appStore.saveCurrentScript()"
        >
          <Save class="w-4 h-4" />
          保存
        </button>
        <button
          class="btn-solid btn-danger px-3 py-1.5 text-sm flex items-center gap-1.5"
          @click="emit('deleteCurrent')"
        >
          <Trash2 class="w-4 h-4" />
          删除
        </button>
      </div>
    </div>
  </div>
</template>
