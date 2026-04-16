<script setup lang="ts">
import { reactive, onMounted } from "vue";
import { useRouter } from "vue-router";
import { ArrowLeft, FolderSearch } from "lucide-vue-next";
import { appStore } from "../stores/appStore";

const router = useRouter();
const { state } = appStore;

const form = reactive({
  scriptsRoot: "",
  logsRoot: "",
});

onMounted(async () => {
  if (!state.startupInfo) {
    await appStore.initApp();
  }
  form.scriptsRoot = state.settings.scriptsRoot || "";
  form.logsRoot = state.settings.logsRoot || "";
});

async function pickScriptsRoot() {
  const selected = await appStore.pickFolder(form.scriptsRoot || undefined);
  if (selected) {
    form.scriptsRoot = selected;
  }
}

async function pickLogsRoot() {
  const selected = await appStore.pickFolder(form.logsRoot || undefined);
  if (selected) {
    form.logsRoot = selected;
  }
}

async function saveSettings() {
  try {
    await appStore.saveSettings({
      scriptsRoot: form.scriptsRoot.trim(),
      logsRoot: form.logsRoot.trim(),
    });
    await router.push("/");
  } catch (error) {
    appStore.pushToast("error", error instanceof Error ? error.message : "保存设置失败");
  }
}
</script>

<template>
  <div class="size-full app-shell p-4">
    <div class="h-full rounded-2xl arc-glass-main p-5">
      <div class="flex items-center justify-between mb-6">
        <button
          class="btn-solid btn-neutral px-3 py-1.5 text-sm flex items-center gap-1.5"
          @click="router.push('/')"
        >
          <ArrowLeft class="w-4 h-4" />
          返回
        </button>
        <h1 class="text-lg font-semibold text-slate-900">设置</h1>
      </div>

      <div class="grid gap-5 max-w-3xl">
        <div class="arc-glass-surface rounded-xl p-4">
          <h2 class="text-base font-semibold text-slate-900">脚本目录</h2>
          <p class="text-xs text-slate-600 mt-1">配置后左侧列表会递归显示该目录下所有文件夹与脚本文件。</p>
          <div class="mt-3 flex gap-2">
            <input
              v-model="form.scriptsRoot"
              type="text"
              class="flex-1 px-3 py-2 rounded-lg text-sm arc-input"
              placeholder="例如：D:\\scripts"
            />
            <button
              class="btn-solid btn-neutral px-3 py-2 text-sm flex items-center gap-1.5"
              @click="pickScriptsRoot"
            >
              <FolderSearch class="w-4 h-4" />
              选择
            </button>
          </div>
        </div>

        <div class="arc-glass-surface rounded-xl p-4">
          <h2 class="text-base font-semibold text-slate-900">运行日志目录</h2>
          <p class="text-xs text-slate-600 mt-1">每次运行都会创建单独日志文件，文件名包含脚本路径和脚本名。</p>
          <div class="mt-3 flex gap-2">
            <input
              v-model="form.logsRoot"
              type="text"
              class="flex-1 px-3 py-2 rounded-lg text-sm arc-input"
              placeholder="例如：D:\\script-logs"
            />
            <button
              class="btn-solid btn-neutral px-3 py-2 text-sm flex items-center gap-1.5"
              @click="pickLogsRoot"
            >
              <FolderSearch class="w-4 h-4" />
              选择
            </button>
          </div>
        </div>

        <div class="flex justify-end">
          <button class="btn-solid btn-primary px-4 py-2 text-sm" @click="saveSettings">
            保存设置
          </button>
        </div>
      </div>
    </div>
  </div>
</template>
