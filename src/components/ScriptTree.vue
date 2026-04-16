<script setup lang="ts">
import type { ScriptConfig, ScriptNode, ScriptRuntimeRecord } from "../types";
import TreeNodeItem from "./TreeNodeItem.vue";

defineProps<{
  nodes: ScriptNode[];
  selectedPath: string | null;
  runtimeRecords: Record<string, ScriptRuntimeRecord>;
  scriptConfigs: Record<string, ScriptConfig>;
  runningPaths: Set<string>;
  editingPath: string | null;
}>();

const emit = defineEmits<{
  select: [relativePath: string];
  contextmenu: [payload: { x: number; y: number; node?: ScriptNode }];
  renameSubmit: [payload: { relativePath: string; newName: string }];
  renameCancel: [];
}>();

function onBlankContextMenu(event: MouseEvent) {
  event.preventDefault();
  emit("contextmenu", { x: event.clientX, y: event.clientY });
}
</script>

<template>
  <div class="h-full overflow-auto p-2" @contextmenu.self="onBlankContextMenu">
    <div v-if="nodes.length === 0" class="p-4 text-xs text-slate-500">脚本目录为空</div>

    <TreeNodeItem
      v-for="node in nodes"
      :key="node.relativePath"
      :node="node"
      :depth="0"
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
</template>
