<script setup lang="ts">
import { appStore } from "../stores/appStore";

const { state, removeToast } = appStore;
</script>

<template>
  <div class="fixed top-4 right-4 z-[9999] flex flex-col gap-2 w-88">
    <div
      v-for="item in state.toasts"
      :key="item.id"
      class="rounded-xl border px-3 py-2 shadow-lg backdrop-blur-md text-sm flex items-start justify-between gap-3"
      :class="{
        'bg-green-50/90 border-green-300 text-green-800': item.type === 'success',
        'bg-red-50/90 border-red-300 text-red-800': item.type === 'error',
        'bg-amber-50/90 border-amber-300 text-amber-800': item.type === 'warning',
        'bg-white/90 border-gray-300 text-gray-800': item.type === 'info',
      }"
    >
      <span class="leading-5">{{ item.message }}</span>
      <button class="text-xs opacity-70 hover:opacity-100" @click="removeToast(item.id)">关闭</button>
    </div>
  </div>
</template>
