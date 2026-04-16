<script setup lang="ts">
import { computed, nextTick, ref, watch } from "vue";
import { ChevronDown, ChevronRight, Folder, FileCode, LoaderCircle, CircleCheck, CircleX } from "lucide-vue-next";
import type { ScriptNode, ScriptRuntimeRecord } from "../types";

defineOptions({
  name: "TreeNodeItem",
});

const props = defineProps<{
  node: ScriptNode;
  depth: number;
  selectedPath: string | null;
  runtimeRecords: Record<string, ScriptRuntimeRecord>;
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
const isRunning = computed(() => props.runningPaths.has(props.node.relativePath));
const isEditing = computed(() => props.editingPath === props.node.relativePath);
const createdText = computed(() => {
  if (!props.node.createdAtMs) {
    return "";
  }
  return new Date(props.node.createdAtMs).toLocaleString("zh-CN");
});
const runText = computed(() => {
  if (!runtime.value?.lastRunAt) {
    return "未运行";
  }
  const status = runtime.value.lastStatus === "success" ? "成功" : "失败";
  return `${new Date(runtime.value.lastRunAt).toLocaleString("zh-CN")} · ${status}`;
});

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
  <div>
    <button
      class="w-full text-left px-2 py-1 rounded-md flex items-center gap-2 hover:bg-white/60"
      :class="selectedPath === node.relativePath ? 'bg-white/80 ring-1 ring-indigo-200/70' : ''"
      :style="{ paddingLeft: `${depth * 12 + 8}px` }"
      @click="isEditing ? null : node.kind === 'file' ? emit('select', node.relativePath) : (expanded = !expanded)"
      @contextmenu="onRightClick"
    >
      <ChevronDown v-if="node.kind === 'folder' && expanded" class="w-4 h-4 text-gray-500" />
      <ChevronRight v-else-if="node.kind === 'folder'" class="w-4 h-4 text-gray-500" />
      <span v-else class="w-4"></span>

      <Folder v-if="node.kind === 'folder'" class="w-4 h-4 text-indigo-600" />
      <FileCode v-else class="w-4 h-4 text-gray-700" />

      <input
        v-if="isEditing"
        ref="renameInputRef"
        v-model="editingName"
        class="flex-1 min-w-0 text-sm text-gray-800 bg-white border border-indigo-300 rounded px-1.5 py-0.5"
        @click.stop
        @keydown.enter.prevent="submitRename"
        @keydown.esc.prevent="emit('renameCancel')"
        @blur="submitRename"
      />
      <div v-else class="min-w-0 flex-1">
        <div class="truncate text-sm text-gray-800">{{ node.name }}</div>
        <div v-if="node.kind === 'file'" class="text-[11px] text-gray-500 leading-4 mt-0.5 truncate">
          创建：{{ createdText || "-" }}
        </div>
        <div v-if="node.kind === 'file'" class="text-[11px] leading-4 truncate">
          <span class="text-gray-500">上次运行：</span>
          <span
            :class="{
              'text-blue-600': isRunning,
              'text-green-600': runtime?.lastStatus === 'success',
              'text-red-600': runtime?.lastStatus === 'failed' || runtime?.lastStatus === 'error',
              'text-gray-500': !runtime?.lastStatus,
            }"
          >
            {{ isRunning ? "运行中" : runText }}
          </span>
        </div>
      </div>

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
