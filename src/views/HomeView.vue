<script setup lang="ts">
import { computed, onMounted, onUnmounted, reactive, ref } from "vue";
import { useRouter } from "vue-router";
import { FolderPlus, Plus, RefreshCw, Settings } from "lucide-vue-next";
import ScriptTree from "../components/ScriptTree.vue";
import ScriptEditorPanel from "../components/ScriptEditorPanel.vue";
import type { ScriptNode } from "../types";
import { appStore } from "../stores/appStore";
import { api } from "../services/api";

const router = useRouter();
const { state } = appStore;
const renamingPath = ref<string | null>(null);

const menuState = reactive<{
  visible: boolean;
  x: number;
  y: number;
  node?: ScriptNode;
}>({
  visible: false,
  x: 0,
  y: 0,
});

const configState = reactive({
  visible: false,
  targetPath: "",
  interpreterPath: "",
  args: "",
});

const currentRuntimeText = computed(() => {
  if (!state.selectedScriptPath) {
    return "";
  }
  const runtime = state.runtimeRecords[state.selectedScriptPath];
  if (!runtime) {
    return "尚未运行";
  }

  const date = new Date(runtime.lastRunAt).toLocaleString("zh-CN");
  const status = runtime.lastStatus === "success" ? "成功" : "失败";
  return `${date} · ${status} · 耗时 ${runtime.lastDurationMs}ms`;
});

const isCurrentRunning = computed(() => {
  return !!state.selectedScriptPath && state.runningPaths.has(state.selectedScriptPath);
});

const contextActions = computed(() => {
  const node = menuState.node;
  const actions: Array<{ key: string; label: string; handler: () => void }> = [];

  if (!node) {
    actions.push(
      { key: "new-script", label: "新增脚本", handler: () => createScriptAt("") },
      { key: "new-folder", label: "新增文件夹", handler: () => createFolderAt("") },
    );
    return actions;
  }

  if (node.kind === "folder") {
    actions.push(
      { key: "folder-new-script", label: "新增脚本", handler: () => createScriptAt(node.relativePath) },
      { key: "folder-new-folder", label: "新增子文件夹", handler: () => createFolderAt(node.relativePath) },
      { key: "folder-rename", label: "重命名", handler: () => startRename(node.relativePath) },
      { key: "folder-open", label: "打开位置", handler: () => appStore.openPath(node.relativePath) },
      {
        key: "folder-delete",
        label: "删除",
        handler: async () => {
          if (globalThis.confirm(`确定删除文件夹 ${node.name} 吗？`)) {
            await appStore.deletePath(node.relativePath);
            appStore.pushToast("success", "文件夹已删除");
          }
        },
      },
    );
    return actions;
  }

  actions.push(
    { key: "run", label: "运行", handler: () => runFile(node.relativePath) },
    { key: "rename", label: "重命名", handler: () => startRename(node.relativePath) },
    { key: "config", label: "脚本配置", handler: () => openScriptConfig(node.relativePath) },
    { key: "open", label: "打开位置", handler: () => appStore.openPath(node.relativePath) },
    {
      key: "delete",
      label: "删除",
      handler: async () => {
        if (globalThis.confirm(`确定删除脚本 ${node.name} 吗？`)) {
          await appStore.deletePath(node.relativePath);
          appStore.pushToast("success", "脚本已删除");
        }
      },
    },
  );

  return actions;
});

function showMenu(payload: { x: number; y: number; node?: ScriptNode }) {
  menuState.visible = true;
  menuState.x = payload.x;
  menuState.y = payload.y;
  menuState.node = payload.node;
}

function hideMenu() {
  menuState.visible = false;
  menuState.node = undefined;
}

function findNode(nodes: ScriptNode[], relativePath: string): ScriptNode | undefined {
  for (const node of nodes) {
    if (node.relativePath === relativePath) {
      return node;
    }
    if (node.kind === "folder" && node.children?.length) {
      const found = findNode(node.children, relativePath);
      if (found) {
        return found;
      }
    }
  }
  return undefined;
}

function getChildrenAt(parentRelativePath: string): ScriptNode[] {
  if (!parentRelativePath) {
    return state.treeNodes;
  }
  const folder = findNode(state.treeNodes, parentRelativePath);
  if (!folder || folder.kind !== "folder") {
    return [];
  }
  return folder.children || [];
}

function getNextDefaultName(parentRelativePath: string, kind: "file" | "folder"): string {
  const prefix = kind === "folder" ? "未命名文件夹" : "未命名脚本";
  const siblings = getChildrenAt(parentRelativePath);
  const existing = new Set(siblings.map((x) => x.name));

  let seq = 1;
  while (existing.has(`${prefix}${seq}`)) {
    seq += 1;
  }
  return `${prefix}${seq}`;
}

async function ensureScriptsRootBeforeCreate() {
  if (state.settings.scriptsRoot) {
    return true;
  }

  appStore.pushToast("warning", "请先在设置页配置脚本目录");
  await router.push("/settings");
  return false;
}

async function createScriptAt(parentRelativePath: string) {
  hideMenu();
  if (!(await ensureScriptsRootBeforeCreate())) {
    return;
  }

  const fileName = getNextDefaultName(parentRelativePath, "file");

  try {
    await appStore.createScript(parentRelativePath, fileName);
    appStore.pushToast("success", `脚本创建成功：${fileName}`);
  } catch (error) {
    appStore.pushToast("error", error instanceof Error ? error.message : "创建脚本失败");
  }
}

async function createFolderAt(parentRelativePath: string) {
  hideMenu();
  if (!(await ensureScriptsRootBeforeCreate())) {
    return;
  }

  const folderName = getNextDefaultName(parentRelativePath, "folder");

  try {
    await appStore.createFolder(parentRelativePath, folderName);
    appStore.pushToast("success", `文件夹创建成功：${folderName}`);
  } catch (error) {
    appStore.pushToast("error", error instanceof Error ? error.message : "创建文件夹失败");
  }
}

function startRename(relativePath: string) {
  hideMenu();
  renamingPath.value = relativePath;
}

async function submitRename(payload: { relativePath: string; newName: string }) {
  if (!payload.newName) {
    appStore.pushToast("warning", "名称不能为空");
    renamingPath.value = null;
    return;
  }

  try {
    const result = await api.renamePath(payload.relativePath, payload.newName);
    const oldPath = payload.relativePath;

    let nextSelected: string | null = null;
    if (state.selectedScriptPath) {
      if (state.selectedScriptPath === oldPath) {
        nextSelected = result.newRelativePath;
      } else if (state.selectedScriptPath.startsWith(`${oldPath}/`)) {
        nextSelected = `${result.newRelativePath}${state.selectedScriptPath.slice(oldPath.length)}`;
      }
    }

    renamingPath.value = null;
    await appStore.refreshTree();

    if (nextSelected) {
      await appStore.selectScript(nextSelected);
    }

    appStore.pushToast("success", "重命名成功");
  } catch (error) {
    appStore.pushToast("error", error instanceof Error ? error.message : "重命名失败");
    renamingPath.value = null;
  }
}

function cancelRename() {
  renamingPath.value = null;
}

async function openScriptConfig(relativePath?: string) {
  hideMenu();
  const targetPath = relativePath || state.selectedScriptPath;
  if (!targetPath) {
    return;
  }

  try {
    const cfg = await api.getScriptConfig(targetPath);
    configState.targetPath = targetPath;
    configState.interpreterPath = cfg.interpreterPath || "";
    configState.args = cfg.args || "";
    configState.visible = true;
  } catch (error) {
    appStore.pushToast("error", error instanceof Error ? error.message : "读取脚本配置失败");
  }
}

async function pickInterpreterFile() {
  const picked = await api.pickInterpreterFile(configState.interpreterPath || undefined);
  if (picked) {
    configState.interpreterPath = picked;
  }
}

async function saveScriptConfig() {
  if (!configState.targetPath) {
    return;
  }

  try {
    await api.setScriptConfig(configState.targetPath, {
      interpreterPath: configState.interpreterPath.trim(),
      args: configState.args.trim(),
    });

    if (state.selectedScriptPath === configState.targetPath) {
      appStore.updateInterpreter(configState.interpreterPath.trim());
      appStore.updateArgs(configState.args.trim());
    }

    appStore.pushToast("success", "脚本配置已保存");
  } catch (error) {
    appStore.pushToast("error", error instanceof Error ? error.message : "保存脚本配置失败");
  }
}

async function runFile(relativePath: string) {
  hideMenu();
  if (state.selectedScriptPath !== relativePath) {
    await appStore.selectScript(relativePath);
  }
  await appStore.runCurrentScript();
}

async function onDeleteCurrent() {
  if (!state.selectedScriptPath) {
    return;
  }
  if (!globalThis.confirm("确定删除当前脚本吗？")) {
    return;
  }

  const target = state.selectedScriptPath;
  await appStore.deletePath(target);
  appStore.pushToast("success", "脚本已删除");
}

onMounted(async () => {
  if (!state.startupInfo) {
    await appStore.initApp();
  }
  window.addEventListener("click", hideMenu);
  window.addEventListener("focus", appStore.refreshTree);
});

onUnmounted(() => {
  window.removeEventListener("click", hideMenu);
  window.removeEventListener("focus", appStore.refreshTree);
});
</script>

<template>
  <div class="size-full flex flex-col bg-gradient-to-br from-indigo-100 via-purple-50 to-pink-100">
    <div class="h-12 bg-white/40 backdrop-blur-2xl border-b border-white/20 flex items-center px-4 shadow-sm">
      <div class="flex items-center justify-between w-full">
        <div class="text-sm font-semibold text-gray-800">脚本管理器</div>
        <button
          class="px-3 py-1.5 rounded-lg text-sm bg-white/70 border border-white/70 hover:bg-white flex items-center gap-1.5"
          @click="router.push('/settings')"
        >
          <Settings class="w-4 h-4" />
          设置
        </button>
      </div>
    </div>

    <div class="flex-1 flex overflow-hidden p-3 gap-3">
      <div class="w-80 rounded-2xl overflow-hidden shadow-2xl shadow-indigo-200/50 ring-1 ring-white/40 bg-white/30 backdrop-blur-2xl">
        <div class="px-3 py-2 border-b border-white/25 bg-white/40">
          <div class="flex items-center justify-between">
            <div>
              <p class="text-sm font-semibold text-gray-800">脚本列表</p>
              <p class="text-xs text-gray-600 truncate max-w-52">{{ state.settings.scriptsRoot || '未配置脚本目录' }}</p>
            </div>
            <div class="flex items-center gap-1">
              <button class="p-1.5 rounded-md hover:bg-white/60" title="刷新列表" @click="appStore.refreshTree()">
                <RefreshCw class="w-4 h-4 text-indigo-600" />
              </button>
              <button class="p-1.5 rounded-md hover:bg-white/60" title="新增脚本" @click="createScriptAt('')">
                <Plus class="w-4 h-4 text-indigo-600" />
              </button>
              <button class="p-1.5 rounded-md hover:bg-white/60" title="新增文件夹" @click="createFolderAt('')">
                <FolderPlus class="w-4 h-4 text-indigo-600" />
              </button>
            </div>
          </div>
        </div>

        <ScriptTree
          :nodes="state.treeNodes"
          :selected-path="state.selectedScriptPath"
          :runtime-records="state.runtimeRecords"
          :running-paths="state.runningPaths"
          :editing-path="renamingPath"
          @select="appStore.selectScript"
          @contextmenu="showMenu"
          @rename-submit="submitRename"
          @rename-cancel="cancelRename"
        />
      </div>

      <div class="flex-1 rounded-2xl overflow-hidden shadow-2xl shadow-indigo-200/50 ring-1 ring-white/40">
        <ScriptEditorPanel
          :runtime-text="currentRuntimeText"
          :is-running="isCurrentRunning"
          @delete-current="onDeleteCurrent"
          @open-config="openScriptConfig()"
        />
      </div>
    </div>

    <div
      v-if="menuState.visible"
      class="fixed z-[9998] w-32 rounded-lg border border-gray-200 bg-white shadow-xl py-1"
      :style="{ left: `${menuState.x}px`, top: `${menuState.y}px` }"
      @click.stop
    >
      <button
        v-for="action in contextActions"
        :key="action.key"
        class="w-full text-left px-3 py-1.5 text-xs text-gray-700 hover:bg-gray-100 whitespace-nowrap"
        @click="action.handler"
      >
        {{ action.label }}
      </button>
    </div>
  </div>

  <div
    v-if="configState.visible"
    class="fixed inset-0 z-[9999] bg-black/30 backdrop-blur-[1px] flex items-center justify-center"
  >
    <div class="w-[560px] max-w-[92vw] rounded-xl bg-white border border-gray-200 shadow-2xl p-4">
      <h3 class="text-base font-semibold text-gray-900">脚本配置</h3>
      <p class="text-xs text-gray-500 mt-1">为脚本配置解释器绝对路径与运行参数</p>

      <div class="mt-4 space-y-3">
        <div>
          <label class="text-sm text-gray-700">解释器绝对路径</label>
          <div class="mt-1 flex gap-2">
            <input
              v-model="configState.interpreterPath"
              type="text"
              class="flex-1 px-3 py-2 rounded-lg border border-gray-300 text-sm"
              placeholder="例如：C:\\Python311\\python.exe"
            />
            <button
              class="px-3 py-2 text-sm rounded-lg border border-gray-300 bg-gray-50 hover:bg-gray-100"
              @click="pickInterpreterFile"
            >
              选择文件
            </button>
          </div>
        </div>

        <div>
          <label class="text-sm text-gray-700">运行参数</label>
          <input
            v-model="configState.args"
            type="text"
            class="mt-1 w-full px-3 py-2 rounded-lg border border-gray-300 text-sm"
            placeholder="例如：--env prod --id 1"
          />
        </div>
      </div>

      <div class="mt-4 flex justify-end gap-2">
        <button
          class="px-3 py-1.5 rounded-lg border border-gray-300 text-sm bg-white hover:bg-gray-50"
          @click="configState.visible = false"
        >
          取消
        </button>
        <button
          class="px-3 py-1.5 rounded-lg text-sm bg-indigo-600 text-white hover:bg-indigo-700"
          @click="saveScriptConfig"
        >
          保存配置
        </button>
      </div>
    </div>
  </div>
</template>
