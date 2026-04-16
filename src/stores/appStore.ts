import { reactive } from "vue";
import { api } from "../services/api";
import type { ScriptConfig, ScriptNode, ScriptRuntimeRecord, SettingsData, StartupInfo, ToastItem } from "../types";

interface AppState {
  startupInfo: StartupInfo | null;
  settings: SettingsData;
  treeNodes: ScriptNode[];
  runtimeRecords: Record<string, ScriptRuntimeRecord>;
  runningPaths: Set<string>;
  selectedScriptPath: string | null;
  selectedScriptContent: string;
  selectedScriptConfig: ScriptConfig;
  dirty: boolean;
  loadingTree: boolean;
  loadingScript: boolean;
  toasts: ToastItem[];
}

const state = reactive<AppState>({
  startupInfo: null,
  settings: {
    scriptsRoot: "",
    logsRoot: "",
  },
  treeNodes: [],
  runtimeRecords: {},
  runningPaths: new Set<string>(),
  selectedScriptPath: null,
  selectedScriptContent: "",
  selectedScriptConfig: {
    interpreterPath: "",
    args: "",
  },
  dirty: false,
  loadingTree: false,
  loadingScript: false,
  toasts: [],
});

let toastId = 1;

function pushToast(type: ToastItem["type"], message: string) {
  const id = toastId++;
  state.toasts.push({ id, type, message });
  setTimeout(() => {
    const idx = state.toasts.findIndex((x) => x.id === id);
    if (idx >= 0) {
      state.toasts.splice(idx, 1);
    }
  }, 3500);
}

function removeToast(id: number) {
  const idx = state.toasts.findIndex((x) => x.id === id);
  if (idx >= 0) {
    state.toasts.splice(idx, 1);
  }
}

function clearSelection() {
  state.selectedScriptPath = null;
  state.selectedScriptContent = "";
  state.selectedScriptConfig = { interpreterPath: "", args: "" };
  state.dirty = false;
}

async function initApp() {
  state.startupInfo = await api.getStartupInfo();
  state.settings = await api.getSettings();
  await refreshTree();

  if (!state.settings.scriptsRoot) {
    pushToast("warning", "未配置脚本目录，请先到设置页配置。未配置前无法新建脚本。");
  }
}

async function refreshTree() {
  state.loadingTree = true;
  try {
    const tree = await api.listTree();
    state.treeNodes = tree.nodes;
    state.runtimeRecords = tree.runtimeRecords || {};
    state.runningPaths = new Set(tree.running || []);

    if (state.selectedScriptPath && !containsFile(state.treeNodes, state.selectedScriptPath)) {
      clearSelection();
    }
  } finally {
    state.loadingTree = false;
  }
}

function containsFile(nodes: ScriptNode[], target: string): boolean {
  for (const node of nodes) {
    if (node.kind === "file" && node.relativePath === target) {
      return true;
    }
    if (node.kind === "folder" && node.children?.length) {
      if (containsFile(node.children, target)) {
        return true;
      }
    }
  }
  return false;
}

async function selectScript(relativePath: string) {
  state.loadingScript = true;
  try {
    const [content, config] = await Promise.all([
      api.readFile(relativePath),
      api.getScriptConfig(relativePath),
    ]);
    state.selectedScriptPath = relativePath;
    state.selectedScriptContent = content;
    state.selectedScriptConfig = config;
    state.dirty = false;
  } finally {
    state.loadingScript = false;
  }
}

async function saveCurrentScript() {
  if (!state.selectedScriptPath) {
    return;
  }
  await api.writeFile(state.selectedScriptPath, state.selectedScriptContent);
  state.dirty = false;
  pushToast("success", "脚本已保存");
}

async function saveCurrentScriptConfig() {
  if (!state.selectedScriptPath) {
    return;
  }
  await api.setScriptConfig(state.selectedScriptPath, state.selectedScriptConfig);
}

async function runCurrentScript() {
  if (!state.selectedScriptPath) {
    return;
  }
  if (state.runningPaths.has(state.selectedScriptPath)) {
    pushToast("warning", "脚本正在运行中，请稍候");
    return;
  }

  if (!state.selectedScriptConfig.interpreterPath) {
    pushToast("warning", "请先配置解释器绝对路径");
    return;
  }

  state.runningPaths.add(state.selectedScriptPath);

  try {
    const result = await api.runScript(state.selectedScriptPath);
    if (result.success) {
      pushToast("success", `运行成功，日志：${result.logPath}`);
    } else {
      pushToast("error", `运行失败（退出码 ${result.exitCode}），日志：${result.logPath}`);
    }
    await refreshTree();
  } catch (error) {
    const message = error instanceof Error ? error.message : "运行失败";
    pushToast("error", message.includes("日志") ? message : `运行失败。请检查日志目录：${state.settings.logsRoot || "应用默认日志目录"}`);
    await refreshTree();
  } finally {
    state.runningPaths.delete(state.selectedScriptPath);
  }
}

async function createScript(parentRelativePath: string, fileName: string) {
  await api.createFile(parentRelativePath, fileName);
  await refreshTree();
}

async function createFolder(parentRelativePath: string, folderName: string) {
  await api.createFolder(parentRelativePath, folderName);
  await refreshTree();
}

async function deletePath(relativePath: string) {
  await api.deletePath(relativePath);
  if (state.selectedScriptPath === relativePath) {
    clearSelection();
  }
  await refreshTree();
}

async function openPath(relativePath: string) {
  await api.openPath(relativePath);
}

async function saveSettings(next: SettingsData) {
  await api.saveSettings(next);
  state.settings = { ...next };
  await refreshTree();
  pushToast("success", "设置已保存");
}

async function pickFolder(defaultPath?: string) {
  return await api.pickFolder(defaultPath);
}

function updateEditorContent(content: string) {
  state.selectedScriptContent = content;
  state.dirty = true;
}

function updateInterpreter(interpreterPath: string) {
  state.selectedScriptConfig.interpreterPath = interpreterPath;
}

function updateArgs(args: string) {
  state.selectedScriptConfig.args = args;
}

export const appStore = {
  state,
  pushToast,
  removeToast,
  initApp,
  refreshTree,
  selectScript,
  saveCurrentScript,
  saveCurrentScriptConfig,
  runCurrentScript,
  createScript,
  createFolder,
  deletePath,
  openPath,
  saveSettings,
  pickFolder,
  updateEditorContent,
  updateInterpreter,
  updateArgs,
};
