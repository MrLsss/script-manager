const { contextBridge, ipcRenderer } = require("electron");

contextBridge.exposeInMainWorld("electronAPI", {
  getStartupInfo: () => ipcRenderer.invoke("app:get-startup-info"),
  getSettings: () => ipcRenderer.invoke("settings:get"),
  saveSettings: (payload) => ipcRenderer.invoke("settings:save", payload),
  pickFolder: (defaultPath) => ipcRenderer.invoke("dialog:pick-folder", defaultPath),
  pickInterpreterFile: (defaultPath) => ipcRenderer.invoke("dialog:pick-interpreter-file", defaultPath),

  listTree: () => ipcRenderer.invoke("scripts:list-tree"),
  readFile: (relativePath) => ipcRenderer.invoke("scripts:read-file", relativePath),
  writeFile: (payload) => ipcRenderer.invoke("scripts:write-file", payload),
  createFile: (payload) => ipcRenderer.invoke("scripts:create-file", payload),
  createFolder: (payload) => ipcRenderer.invoke("scripts:create-folder", payload),
  deletePath: (relativePath) => ipcRenderer.invoke("scripts:delete-path", relativePath),
  openPath: (relativePath) => ipcRenderer.invoke("scripts:open-path", relativePath),
  renamePath: (payload) => ipcRenderer.invoke("scripts:rename-path", payload),

  getScriptConfig: (relativePath) => ipcRenderer.invoke("script:get-config", relativePath),
  setScriptConfig: (payload) => ipcRenderer.invoke("script:set-config", payload),
  runScript: (relativePath) => ipcRenderer.invoke("script:run", relativePath),
});


