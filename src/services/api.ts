import type { RunResult, ScriptConfig, SettingsData, StartupInfo, TreeResult } from "../types";

export const api = {
  getStartupInfo(): Promise<StartupInfo> {
    return window.electronAPI.getStartupInfo();
  },
  getSettings(): Promise<SettingsData> {
    return window.electronAPI.getSettings();
  },
  saveSettings(payload: SettingsData): Promise<{ ok: boolean }> {
    return window.electronAPI.saveSettings(payload);
  },
  pickFolder(defaultPath?: string): Promise<string> {
    return window.electronAPI.pickFolder(defaultPath);
  },
  pickInterpreterFile(defaultPath?: string): Promise<string> {
    return window.electronAPI.pickInterpreterFile(defaultPath);
  },
  listTree(): Promise<TreeResult> {
    return window.electronAPI.listTree();
  },
  readFile(relativePath: string): Promise<string> {
    return window.electronAPI.readFile(relativePath);
  },
  writeFile(relativePath: string, content: string): Promise<{ ok: boolean }> {
    return window.electronAPI.writeFile({ relativePath, content });
  },
  createFile(parentRelativePath: string, fileName: string): Promise<{ ok: boolean }> {
    return window.electronAPI.createFile({ parentRelativePath, fileName });
  },
  createFolder(parentRelativePath: string, folderName: string): Promise<{ ok: boolean }> {
    return window.electronAPI.createFolder({ parentRelativePath, folderName });
  },
  deletePath(relativePath: string): Promise<{ ok: boolean }> {
    return window.electronAPI.deletePath(relativePath);
  },
  openPath(relativePath: string): Promise<{ ok: boolean }> {
    return window.electronAPI.openPath(relativePath);
  },
  renamePath(relativePath: string, newName: string): Promise<{ ok: boolean; newRelativePath: string }> {
    return window.electronAPI.renamePath({ relativePath, newName });
  },
  getScriptConfig(relativePath: string): Promise<ScriptConfig> {
    return window.electronAPI.getScriptConfig(relativePath);
  },
  setScriptConfig(relativePath: string, config: ScriptConfig): Promise<{ ok: boolean; config: ScriptConfig }> {
    return window.electronAPI.setScriptConfig({ relativePath, ...config });
  },
  runScript(relativePath: string): Promise<RunResult> {
    return window.electronAPI.runScript(relativePath);
  },
};


