/// <reference types="vite/client" />

import type { RunResult, ScriptConfig, SettingsData, StartupInfo, TreeResult } from "./types";

declare global {
  interface Window {
    electronAPI: {
      getStartupInfo: () => Promise<StartupInfo>;
      getSettings: () => Promise<SettingsData>;
      saveSettings: (payload: SettingsData) => Promise<{ ok: boolean }>;
      pickFolder: (defaultPath?: string) => Promise<string>;
      pickInterpreterFile: (defaultPath?: string) => Promise<string>;
      listTree: () => Promise<TreeResult>;
      readFile: (relativePath: string) => Promise<string>;
      writeFile: (payload: { relativePath: string; content: string }) => Promise<{ ok: boolean }>;
      createFile: (payload: { parentRelativePath: string; fileName: string }) => Promise<{ ok: boolean }>;
      createFolder: (payload: { parentRelativePath: string; folderName: string }) => Promise<{ ok: boolean }>;
      deletePath: (relativePath: string) => Promise<{ ok: boolean }>;
      openPath: (relativePath: string) => Promise<{ ok: boolean }>;
      renamePath: (payload: { relativePath: string; newName: string }) => Promise<{ ok: boolean; newRelativePath: string }>;
      getScriptConfig: (relativePath: string) => Promise<ScriptConfig>;
      setScriptConfig: (payload: {
        relativePath: string;
        interpreterPath: string;
        args: string;
      }) => Promise<{ ok: boolean }>;
      runScript: (relativePath: string) => Promise<RunResult>;
    };
  }
}

export {};


