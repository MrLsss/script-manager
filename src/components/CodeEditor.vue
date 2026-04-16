<script setup lang="ts">
import { EditorView, keymap, lineNumbers, type ViewUpdate } from "@codemirror/view";
import { EditorState } from "@codemirror/state";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { bracketMatching, foldGutter, indentOnInput, syntaxHighlighting } from "@codemirror/language";
import { python } from "@codemirror/lang-python";
import { javascript } from "@codemirror/lang-javascript";
import { json } from "@codemirror/lang-json";
import { markdown } from "@codemirror/lang-markdown";
import { StreamLanguage } from "@codemirror/language";
import { shell as shellMode } from "@codemirror/legacy-modes/mode/shell";
import { oneDarkHighlightStyle } from "@codemirror/theme-one-dark";
import { onBeforeUnmount, onMounted, ref, watch } from "vue";

const props = defineProps<{
  modelValue: string;
  fileName: string;
  readOnly?: boolean;
}>();

const emit = defineEmits<{
  "update:modelValue": [value: string];
}>();

const containerRef = ref<HTMLDivElement | null>(null);
let view: EditorView | null = null;

function languageByFile(fileName: string) {
  const lower = fileName.toLowerCase();
  if (lower.endsWith(".py")) {
    return python();
  }
  if (lower.endsWith(".json")) {
    return json();
  }
  if (lower.endsWith(".md") || lower.endsWith(".markdown")) {
    return markdown();
  }
  if (lower.endsWith(".sh") || lower.endsWith(".bash") || lower.endsWith(".zsh") || lower.endsWith(".ps1") || lower.endsWith(".bat") || lower.endsWith(".cmd")) {
    return StreamLanguage.define(shellMode);
  }
  return javascript({ typescript: lower.endsWith(".ts") || lower.endsWith(".tsx") });
}

function createEditor() {
  if (!containerRef.value) {
    return;
  }

  const updateListener = EditorView.updateListener.of((update: ViewUpdate) => {
    if (update.docChanged) {
      emit("update:modelValue", update.state.doc.toString());
    }
  });

  const state = EditorState.create({
    doc: props.modelValue,
    extensions: [
      lineNumbers(),
      foldGutter(),
      history(),
      bracketMatching(),
      indentOnInput(),
      keymap.of([...defaultKeymap, ...historyKeymap]),
      syntaxHighlighting(oneDarkHighlightStyle),
      languageByFile(props.fileName),
      updateListener,
      EditorView.theme({
        "&": {
          height: "100%",
          fontSize: "13px",
          backgroundColor: "transparent",
        },
        ".cm-scroller": {
          overflow: "auto",
          fontFamily: "Consolas, 'Courier New', monospace",
        },
        ".cm-content": {
          minHeight: "100%",
          padding: "12px",
        },
        ".cm-gutters": {
          backgroundColor: "rgba(255,255,255,0.6)",
          border: "none",
        },
      }),
      EditorState.readOnly.of(!!props.readOnly),
    ],
  });

  view = new EditorView({
    state,
    parent: containerRef.value,
  });
}

watch(
  () => props.modelValue,
  (value) => {
    if (!view) {
      return;
    }
    const current = view.state.doc.toString();
    if (current !== value) {
      view.dispatch({
        changes: { from: 0, to: current.length, insert: value },
      });
    }
  },
);

watch(
  () => props.fileName,
  () => {
    if (!view) {
      return;
    }
    view.destroy();
    view = null;
    createEditor();
  },
);

onMounted(() => {
  createEditor();
});

onBeforeUnmount(() => {
  if (view) {
    view.destroy();
    view = null;
  }
});
</script>

<template>
  <div ref="containerRef" class="h-full w-full"></div>
</template>
