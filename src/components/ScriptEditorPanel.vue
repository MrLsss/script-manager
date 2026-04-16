<script setup lang="ts">
import { computed } from "vue";
import { Clock, FolderOpen, Play, Save, Settings, Trash2 } from "lucide-vue-next";
import { appStore } from "../stores/appStore";
import CodeEditor from "./CodeEditor.vue";

defineProps<{
  runtimeText: string;
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
  return new Date(node.createdAtMs).toLocaleString("zh-CN");
});

function openLocation() {
  if (!state.selectedScriptPath) {
    return;
  }
  appStore.openPath(state.selectedScriptPath);
}
</script>

<template>
  <div v-if="!state.selectedScriptPath" class="h-full flex items-center justify-center text-gray-500 text-sm">
    请选择左侧脚本开始编辑
  </div>

  <div v-else class="h-full flex flex-col bg-white/35 backdrop-blur-xl">
    <div class="px-4 py-3 border-b border-white/30 bg-white/40">
      <div class="flex items-center justify-between gap-3">
        <div class="min-w-0">
          <h2 class="text-base font-semibold text-gray-900 truncate">{{ selectedFileName }}</h2>
          <p class="text-xs text-gray-600 mt-1 truncate">{{ state.selectedScriptPath }}</p>
        </div>
        <button
          class="px-3 py-1.5 rounded-lg bg-indigo-600 text-white text-sm flex items-center gap-2 hover:bg-indigo-700 disabled:opacity-60 disabled:cursor-not-allowed"
          :disabled="isRunning"
          @click="appStore.runCurrentScript()"
        >
          <Play class="w-4 h-4" />
          {{ isRunning ? "运行中" : "运行" }}
        </button>
      </div>

      <div class="mt-2 text-xs text-gray-600 flex flex-col gap-1">
        <div class="flex items-center gap-2">
          <Clock class="w-3.5 h-3.5" />
          <span>创建时间：{{ createdAtText || "-" }}</span>
        </div>
        <div class="flex items-center gap-2">
          <Clock class="w-3.5 h-3.5" />
          <span>上次运行：{{ runtimeText }}</span>
        </div>
      </div>
    </div>

    <div class="flex-1 p-3 overflow-hidden">
      <div class="h-full rounded-xl overflow-hidden border border-white/30 bg-white/65">
        <CodeEditor
          :model-value="state.selectedScriptContent"
          :file-name="selectedFileName"
          @update:model-value="appStore.updateEditorContent"
        />
      </div>
    </div>

    <div class="px-4 py-3 border-t border-white/30 bg-white/40 flex items-center justify-between gap-2 flex-wrap">
      <div class="flex items-center gap-2 flex-wrap">
        <button
          class="px-3 py-1.5 rounded-lg text-sm bg-white/70 border border-white/70 hover:bg-white flex items-center gap-1.5"
          @click="openLocation"
        >
          <FolderOpen class="w-4 h-4" />
          打开位置
        </button>
        <button
          class="px-3 py-1.5 rounded-lg text-sm bg-white/70 border border-white/70 hover:bg-white flex items-center gap-1.5"
          @click="emit('openConfig')"
        >
          <Settings class="w-4 h-4" />
          脚本配置
        </button>
      </div>

      <div class="flex items-center gap-2">
        <button
          class="px-3 py-1.5 rounded-lg text-sm bg-indigo-50 border border-indigo-200 text-indigo-700 hover:bg-indigo-100 flex items-center gap-1.5"
          :disabled="!state.dirty"
          @click="appStore.saveCurrentScript()"
        >
          <Save class="w-4 h-4" />
          保存
        </button>
        <button
          class="px-3 py-1.5 rounded-lg text-sm bg-red-50 border border-red-200 text-red-700 hover:bg-red-100 flex items-center gap-1.5"
          @click="emit('deleteCurrent')"
        >
          <Trash2 class="w-4 h-4" />
          删除
        </button>
      </div>
    </div>
  </div>
</template>
