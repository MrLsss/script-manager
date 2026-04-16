<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { ChevronDown, ChevronRight, Folder, FileCode, LoaderCircle, CircleCheck, CircleX, Clock3 } from "lucide-vue-next";
import type { ScriptConfig, ScriptNode, ScriptRuntimeRecord } from "../types";

defineOptions({
  name: "TreeNodeItem",
});

const props = defineProps<{
  node: ScriptNode;
  depth: number;
  selectedPath: string | null;
  runtimeRecords: Record<string, ScriptRuntimeRecord>;
  scriptConfigs: Record<string, ScriptConfig>;
  runningPaths: Set<string>;
  editingPath: string | null;
}>();

const emit = defineEmits<{
  select: [relativePath: string];
  contextmenu: [payload: { x: number; y: number; node: ScriptNode }];
  renameSubmit: [payload: { relativePath: string; newName: string }];
  renameCancel: [];
}>();

const expanded = ref(true);
const renameInputRef = ref<HTMLInputElement | null>(null);
const editingName = ref("");

const runtime = computed(() => props.runtimeRecords[props.node.relativePath]);
const scriptConfig = computed(() => props.scriptConfigs[props.node.relativePath]);
const isRunning = computed(() => props.runningPaths.has(props.node.relativePath));
const isEditing = computed(() => props.editingPath === props.node.relativePath);
const isNodeSelected = computed(() => props.selectedPath === props.node.relativePath);
const isFolderActive = computed(() => {
  if (props.node.kind !== "folder" || !props.selectedPath) {
    return false;
  }
  return props.selectedPath === props.node.relativePath || props.selectedPath.startsWith(`${props.node.relativePath}/`);
});
const runText = computed(() => {
  if (!runtime.value?.lastRunAt) {
    return "尚未运行";
  }
  const status = runtime.value.lastStatus === "success" ? "成功" : "失败";
  return `${status} ${formatRelativeTime(runtime.value.lastRunAt)}`;
});
const runTextClass = computed(() => {
  if (isRunning.value) return "text-blue-600";
  if (!runtime.value?.lastStatus) return "text-slate-500";
  return runtime.value.lastStatus === "success" ? "text-emerald-700" : "text-red-600";
});

const interpreterLabel = computed(() => {
  if (!scriptConfig.value?.interpreterPath) {
    return "";
  }
  return scriptConfig.value.interpreterDisplay || "";
});

const folderScriptCount = computed(() => {
  if (props.node.kind !== "folder") {
    return 0;
  }
  const countFiles = (children?: ScriptNode[]): number => {
    if (!children?.length) return 0;
    return children.reduce((total: number, item) => {
      if (item.kind === "file") return total + 1;
      return total + countFiles(item.children);
    }, 0);
  };
  return countFiles(props.node.children);
});

function formatRelativeTime(ts?: number) {
  if (!ts) {
    return "-";
  }
  const diff = Date.now() - ts;
  const minute = 60 * 1000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (diff < hour) {
    return `${Math.max(1, Math.floor(diff / minute))}分钟前`;
  }
  if (diff < day) {
    return `${Math.floor(diff / hour)}小时前`;
  }
  return `${Math.floor(diff / day)}天前`;
}

watch(isEditing, (v) => {
  if (v) {
    editingName.value = props.node.name;
    nextTick(() => {
      renameInputRef.value?.focus();
      renameInputRef.value?.select();
    });
  }
});

function onRightClick(event: MouseEvent) {
  event.preventDefault();
  event.stopPropagation();
  emit("contextmenu", {
    x: event.clientX,
    y: event.clientY,
    node: props.node,
  });
}

function submitRename() {
  if (!isEditing.value) {
    return;
  }
  emit("renameSubmit", {
    relativePath: props.node.relativePath,
    newName: editingName.value.trim(),
  });
}
</script>

<template>
  <div class="mt-2">
    <button
      class="w-full text-left rounded-2xl border transition-all duration-150"
      :class="
        node.kind === 'file'
          ? [
              'px-3 py-3 flex items-start gap-3 shadow-sm',
              isNodeSelected ? 'arc-file-card-selected' : 'arc-file-card',
            ]
          : [
              'px-3 py-2.5 flex items-center gap-2',
              isFolderActive ? 'arc-folder-row-active' : 'arc-folder-row',
            ]
      "
      :style="{ marginLeft: `${depth * 12}px`, width: `calc(100% - ${depth * 12}px)` }"
      @click="isEditing ? null : node.kind === 'file' ? emit('select', node.relativePath) : (expanded = !expanded)"
      @contextmenu="onRightClick"
    >
      <ChevronDown v-if="node.kind === 'folder' && expanded" class="w-4 h-4 text-slate-500 shrink-0" />
      <ChevronRight v-else-if="node.kind === 'folder'" class="w-4 h-4 text-slate-500 shrink-0" />
      <span v-else class="w-4 shrink-0"></span>

      <span
        class="inline-flex items-center justify-center shrink-0 rounded-lg"
        :class="
          node.kind === 'file'
            ? [isNodeSelected ? 'w-8 h-8 bg-blue-500/18 text-blue-700' : 'w-8 h-8 bg-slate-400/12 text-slate-600']
            : [isFolderActive ? 'text-blue-700' : 'text-slate-600']
        "
      >
        <Folder v-if="node.kind === 'folder'" class="w-4 h-4" />
        <FileCode v-else class="w-4 h-4" />
      </span>

      <input
        v-if="isEditing"
        ref="renameInputRef"
        v-model="editingName"
        class="flex-1 min-w-0 text-sm rounded px-1.5 py-0.5 arc-input"
        @click.stop
        @keydown.enter.prevent="submitRename"
        @keydown.esc.prevent="emit('renameCancel')"
        @blur="submitRename"
      />
      <div v-else class="min-w-0 flex-1">
        <div class="truncate text-sm font-semibold text-slate-800">{{ node.name }}</div>
        <div v-if="node.kind === 'file'" class="flex items-center gap-2 text-[11px] mt-0.5 leading-4 flex-wrap">
          <span class="inline-flex items-center gap-1 text-slate-500">
            <Clock3 class="w-3 h-3" />
            {{ formatRelativeTime(node.createdAtMs) }}
          </span>
          <span
            v-if="interpreterLabel"
            class="inline-flex items-center gap-1 px-1.5 py-0.5 rounded-md bg-blue-500/14 text-blue-700 truncate max-w-[120px]"
          >
            <span class="w-1.5 h-1.5 rounded-full bg-blue-600" />
            {{ interpreterLabel }}
          </span>
        </div>
        <div v-if="node.kind === 'file'" class="text-[11px] leading-4 truncate mt-0.5">
          <span :class="runTextClass">
            {{ isRunning ? "运行中" : runText }}
          </span>
        </div>
      </div>

      <span
        v-if="node.kind === 'folder' && !isEditing"
        class="ml-2 min-w-[1.75rem] h-6 px-2 rounded-md bg-slate-500/12 text-slate-700 text-xs inline-flex items-center justify-center"
      >
        {{ folderScriptCount }}
      </span>

      <span v-if="node.kind === 'file' && !isEditing" class="ml-2 self-start mt-0.5">
        <LoaderCircle v-if="isRunning" class="w-3.5 h-3.5 animate-spin text-blue-600" />
        <CircleCheck v-else-if="runtime?.lastStatus === 'success'" class="w-3.5 h-3.5 text-green-600" />
        <CircleX
          v-else-if="runtime?.lastStatus === 'failed' || runtime?.lastStatus === 'error'"
          class="w-3.5 h-3.5 text-red-600"
        />
      </span>
    </button>

    <div v-if="node.kind === 'folder' && expanded && node.children?.length">
      <TreeNodeItem
        v-for="child in node.children"
        :key="child.relativePath"
        :node="child"
        :depth="depth + 1"
        :selected-path="selectedPath"
        :runtime-records="runtimeRecords"
        :script-configs="scriptConfigs"
        :running-paths="runningPaths"
        :editing-path="editingPath"
        @select="emit('select', $event)"
        @contextmenu="emit('contextmenu', $event)"
        @rename-submit="emit('renameSubmit', $event)"
        @rename-cancel="emit('renameCancel')"
      />
    </div>
  </div>
</template>
