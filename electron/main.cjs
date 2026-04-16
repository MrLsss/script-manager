const {
  app,
  BrowserWindow,
  dialog,
  ipcMain,
  Menu,
  nativeImage,
  shell,
  Tray,
} = require("electron");
const path = require("node:path");
const fs = require("node:fs");
const fsp = require("node:fs/promises");
const { spawn } = require("node:child_process");

const isDev = !app.isPackaged;
const DEV_URL = "http://127.0.0.1:5174";

let mainWindow = null;
let tray = null;
let isQuitting = false;

const runningScripts = new Map();

function getConfigPath() {
  return path.join(app.getPath("userData"), "config.json");
}

function getRunsHistoryPath() {
  return path.join(app.getPath("userData"), "runs-history.jsonl");
}

function getDefaultConfig() {
  return {
    scriptsRoot: "",
    logsRoot: "",
    scriptConfigs: {},
    runtimeRecords: {},
  };
}

async function readJson(filePath, fallback) {
  try {
    const raw = await fsp.readFile(filePath, "utf8");
    return JSON.parse(raw);
  } catch {
    return fallback;
  }
}

async function writeJson(filePath, data) {
  await fsp.mkdir(path.dirname(filePath), { recursive: true });
  await fsp.writeFile(filePath, JSON.stringify(data, null, 2), "utf8");
}

let appConfig = getDefaultConfig();

async function loadConfig() {
  const loaded = await readJson(getConfigPath(), getDefaultConfig());
  appConfig = {
    ...getDefaultConfig(),
    ...loaded,
    scriptConfigs: loaded.scriptConfigs || {},
    runtimeRecords: loaded.runtimeRecords || {},
  };
}

async function saveConfig() {
  await writeJson(getConfigPath(), appConfig);
}

function normalizeRel(relPath) {
  return relPath.split(path.sep).join("/");
}

function isWithin(parentPath, targetPath) {
  const rel = path.relative(parentPath, targetPath);
  return !!rel && !rel.startsWith("..") && !path.isAbsolute(rel);
}

function ensureScriptsRoot() {
  if (!appConfig.scriptsRoot) {
    const err = new Error("未配置脚本目录");
    err.code = "NO_SCRIPTS_ROOT";
    throw err;
  }
}

function resolveInScriptsRoot(relativePath) {
  ensureScriptsRoot();
  const root = path.resolve(appConfig.scriptsRoot);
  const absTarget = path.resolve(root, relativePath || "");

  if (absTarget !== root && !isWithin(root, absTarget)) {
    const err = new Error("目标路径超出脚本目录");
    err.code = "INVALID_PATH";
    throw err;
  }

  return { root, absTarget };
}

async function ensureDir(dirPath) {
  await fsp.mkdir(dirPath, { recursive: true });
}

async function buildTree() {
  if (!appConfig.scriptsRoot) {
    return [];
  }

  const root = path.resolve(appConfig.scriptsRoot);
  const stat = await fsp.stat(root).catch(() => null);
  if (!stat || !stat.isDirectory()) {
    return [];
  }

  async function walk(absDir, relDir) {
    const entries = await fsp.readdir(absDir, { withFileTypes: true });
    const nodes = [];

    for (const entry of entries) {
      const childAbs = path.join(absDir, entry.name);
      const childRel = normalizeRel(path.join(relDir, entry.name));
      const childStat = await fsp.stat(childAbs);
      if (entry.isDirectory()) {
        const children = await walk(childAbs, childRel);
        nodes.push({
          kind: "folder",
          name: entry.name,
          relativePath: childRel,
          absolutePath: childAbs,
          createdAtMs: childStat.birthtimeMs || childStat.ctimeMs,
          updatedAtMs: childStat.mtimeMs,
          children,
        });
      } else if (entry.isFile()) {
        nodes.push({
          kind: "file",
          name: entry.name,
          relativePath: childRel,
          absolutePath: childAbs,
          createdAtMs: childStat.birthtimeMs || childStat.ctimeMs,
          updatedAtMs: childStat.mtimeMs,
        });
      }
    }

    nodes.sort((a, b) => {
      if (a.kind !== b.kind) {
        return a.kind === "folder" ? -1 : 1;
      }
      return a.name.localeCompare(b.name, "zh-Hans-CN");
    });

    return nodes;
  }

  return walk(root, "");
}

function parseArgs(raw) {
  if (!raw || !raw.trim()) {
    return [];
  }
  const result = [];
  const regex = /"([^"]*)"|'([^']*)'|[^\s]+/g;
  let match = regex.exec(raw);
  while (match) {
    result.push(match[1] ?? match[2] ?? match[0]);
    match = regex.exec(raw);
  }
  return result;
}

function sanitizeName(text) {
  return text.replace(/[\\/:*?"<>|]/g, "_").replace(/\s+/g, "_");
}

function formatTime() {
  const now = new Date();
  const pad = (n) => String(n).padStart(2, "0");
  return `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}_${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
}

function trimExeName(binPath) {
  const base = path.basename(binPath || "").toLowerCase();
  return base.replace(/\.(exe|cmd|bat|com|sh|ps1)$/i, "");
}

function normalizeVersion(raw) {
  if (!raw) {
    return "";
  }
  const text = String(raw).trim();
  const match = text.match(/v?(\d+(?:\.\d+){0,3})/i);
  return match ? match[1] : "";
}

function buildInterpreterDisplay(info) {
  if (!info.interpreterName) {
    return "Unknown";
  }
  return info.interpreterVersion ? `${info.interpreterName} ${info.interpreterVersion}` : info.interpreterName;
}

function inferInterpreterByName(interpreterPath) {
  const short = trimExeName(interpreterPath);
  const rules = [
    { test: /^(node|nodejs|bun|tsx|deno)$/, kind: "nodejs", name: "Node.js" },
    { test: /^(python|python\d+(\.\d+)?|py)$/, kind: "python", name: "Python" },
    { test: /^(bash|sh|zsh|dash|ksh)$/, kind: "bash", name: "Bash" },
    { test: /^(pwsh|powershell)$/, kind: "powershell", name: "PowerShell" },
    { test: /^(php)$/, kind: "php", name: "PHP" },
    { test: /^(ruby|irb)$/, kind: "ruby", name: "Ruby" },
    { test: /^(java)$/, kind: "java", name: "Java" },
  ];

  for (const rule of rules) {
    if (rule.test.test(short)) {
      return { interpreterKind: rule.kind, interpreterName: rule.name };
    }
  }

  return { interpreterKind: "unknown", interpreterName: "Unknown" };
}

function getProbeArgsByKind(kind) {
  if (kind === "nodejs") return [["-v"], ["--version"]];
  if (kind === "python") return [["--version"], ["-V"]];
  if (kind === "bash") return [["--version"], ["-version"]];
  if (kind === "powershell") return [["-Version"], ["-v"]];
  if (kind === "php") return [["-v"], ["--version"]];
  if (kind === "ruby") return [["-v"], ["--version"]];
  if (kind === "java") return [["-version"], ["--version"]];
  return [];
}

async function runInterpreterProbe(interpreterPath, probeArgs, timeoutMs = 1800) {
  return await new Promise((resolve) => {
    const child = spawn(interpreterPath, probeArgs, {
      windowsHide: true,
    });

    let stdout = "";
    let stderr = "";
    let settled = false;

    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      child.kill();
      resolve("");
    }, timeoutMs);

    child.stdout.on("data", (chunk) => {
      stdout += chunk?.toString?.() || "";
    });
    child.stderr.on("data", (chunk) => {
      stderr += chunk?.toString?.() || "";
    });

    child.on("error", () => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve("");
    });

    child.on("close", () => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(`${stdout}\n${stderr}`.trim());
    });
  });
}

async function detectInterpreterInfo(interpreterPath) {
  const baseInfo = inferInterpreterByName(interpreterPath);
  const probeMatrix = getProbeArgsByKind(baseInfo.interpreterKind);

  let interpreterVersion = "";
  for (const args of probeMatrix) {
    const output = await runInterpreterProbe(interpreterPath, args);
    const detected = normalizeVersion(output);
    if (detected) {
      interpreterVersion = detected;
      break;
    }
  }

  const result = {
    interpreterKind: baseInfo.interpreterKind,
    interpreterName: baseInfo.interpreterName,
    interpreterVersion,
    interpreterDisplay: "",
  };
  result.interpreterDisplay = buildInterpreterDisplay(result);
  return result;
}

async function appendRunHistory(record) {
  await fsp.mkdir(path.dirname(getRunsHistoryPath()), { recursive: true });
  await fsp.appendFile(getRunsHistoryPath(), `${JSON.stringify(record)}\n`, "utf8");
}

function createTray() {
  if (tray) {
    return;
  }

  const icon = nativeImage
    .createFromDataURL(
      "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAABAAAAAQCAQAAAC1+jfqAAAAz0lEQVR42mP8//8/AyUYTFhY2PD//38GHEA0xMTEBPFfGATiUQxQ8f//f8YQx0aQ/2cYGBhY2R8Qv3//TjB3Y8A0h2YQ7sA4Q1Q8f/4cHh4eJjB0g3g+vXr+f///0YwMDDg+fPnM4g3YkL8+fMnxu3bt2cQ7g4wT0j8+fPnYQYGBkYw9wJi4v///4dxg4ODQ8YfP36cQbQhQqCwYcMGRjC3gSg5f/78YQYGBkYwN0RkYGBgMIP4PwMDA8P//x8MDAwM4M8AAIhXGv1A4E0kAAAAAElFTkSuQmCC",
    )
    .resize({ width: 16, height: 16 });

  tray = new Tray(icon);
  tray.setToolTip("脚本管理器");
  tray.on("double-click", () => {
    if (mainWindow) {
      mainWindow.show();
      mainWindow.focus();
    }
  });

  const menu = Menu.buildFromTemplate([
    {
      label: "显示主窗口",
      click: () => {
        if (mainWindow) {
          mainWindow.show();
          mainWindow.focus();
        }
      },
    },
    { type: "separator" },
    {
      label: "退出",
      click: () => {
        isQuitting = true;
        app.quit();
      },
    },
  ]);

  tray.setContextMenu(menu);
}

function registerIpc() {
  ipcMain.handle("app:get-startup-info", async () => {
    const hasScriptsRoot = !!appConfig.scriptsRoot;
    const scriptsRootValid = hasScriptsRoot
      ? await fsp
          .stat(path.resolve(appConfig.scriptsRoot))
          .then((s) => s.isDirectory())
          .catch(() => false)
      : false;

    return {
      hasScriptsRoot,
      scriptsRootValid,
      message: hasScriptsRoot && scriptsRootValid ? "" : "未配置脚本目录，请先到设置页完成配置。",
    };
  });

  ipcMain.handle("settings:get", async () => {
    return {
      scriptsRoot: appConfig.scriptsRoot,
      logsRoot: appConfig.logsRoot,
    };
  });

  ipcMain.handle("settings:save", async (_, payload) => {
    const nextScriptsRoot = payload.scriptsRoot ? path.resolve(payload.scriptsRoot) : "";
    const nextLogsRoot = payload.logsRoot ? path.resolve(payload.logsRoot) : "";

    if (nextScriptsRoot) {
      const stat = await fsp.stat(nextScriptsRoot).catch(() => null);
      if (!stat || !stat.isDirectory()) {
        const err = new Error("脚本目录不存在或不是文件夹");
        err.code = "INVALID_SCRIPTS_ROOT";
        throw err;
      }
    }

    if (nextLogsRoot) {
      await ensureDir(nextLogsRoot);
    }

    appConfig.scriptsRoot = nextScriptsRoot;
    appConfig.logsRoot = nextLogsRoot;
    await saveConfig();
    return { ok: true };
  });

  ipcMain.handle("dialog:pick-folder", async (_, defaultPath) => {
    const result = await dialog.showOpenDialog(mainWindow, {
      properties: ["openDirectory", "createDirectory"],
      defaultPath: defaultPath || app.getPath("home"),
    });

    if (result.canceled || result.filePaths.length === 0) {
      return "";
    }

    return result.filePaths[0];
  });

  ipcMain.handle("dialog:pick-interpreter-file", async (_, defaultPath) => {
    const result = await dialog.showOpenDialog(mainWindow, {
      properties: ["openFile"],
      defaultPath: defaultPath || app.getPath("home"),
    });

    if (result.canceled || result.filePaths.length === 0) {
      return "";
    }

    return result.filePaths[0];
  });

  ipcMain.handle("scripts:list-tree", async () => {
    const nodes = await buildTree();
    return {
      scriptsRoot: appConfig.scriptsRoot,
      nodes,
      runtimeRecords: appConfig.runtimeRecords,
      running: Array.from(runningScripts.keys()),
      scriptConfigs: appConfig.scriptConfigs || {},
    };
  });

  ipcMain.handle("scripts:read-file", async (_, relativePath) => {
    const { absTarget } = resolveInScriptsRoot(relativePath);
    const stat = await fsp.stat(absTarget);
    if (!stat.isFile()) {
      const err = new Error("只能读取文件");
      err.code = "NOT_FILE";
      throw err;
    }

    return await fsp.readFile(absTarget, "utf8");
  });

  ipcMain.handle("scripts:write-file", async (_, payload) => {
    const { absTarget } = resolveInScriptsRoot(payload.relativePath);
    await fsp.writeFile(absTarget, payload.content ?? "", "utf8");
    return { ok: true };
  });

  ipcMain.handle("scripts:create-file", async (_, payload) => {
    const parentRel = payload.parentRelativePath || "";
    const { absTarget: parentAbs } = resolveInScriptsRoot(parentRel);
    await ensureDir(parentAbs);

    const fileName = payload.fileName || "new-script.js";
    const fileAbs = path.join(parentAbs, fileName);
    const { root } = resolveInScriptsRoot("");
    if (fileAbs !== root && !isWithin(root, fileAbs)) {
      const err = new Error("无效的文件路径");
      err.code = "INVALID_PATH";
      throw err;
    }

    await fsp.writeFile(fileAbs, "", { encoding: "utf8", flag: "wx" });
    return { ok: true };
  });

  ipcMain.handle("scripts:create-folder", async (_, payload) => {
    const parentRel = payload.parentRelativePath || "";
    const { absTarget: parentAbs } = resolveInScriptsRoot(parentRel);
    await ensureDir(parentAbs);

    const folderName = payload.folderName || "new-folder";
    const folderAbs = path.join(parentAbs, folderName);
    const { root } = resolveInScriptsRoot("");
    if (folderAbs !== root && !isWithin(root, folderAbs)) {
      const err = new Error("无效的文件夹路径");
      err.code = "INVALID_PATH";
      throw err;
    }

    await fsp.mkdir(folderAbs, { recursive: false });
    return { ok: true };
  });

  ipcMain.handle("scripts:delete-path", async (_, relativePath) => {
    const { absTarget } = resolveInScriptsRoot(relativePath);
    await fsp.rm(absTarget, { recursive: true, force: false });
    return { ok: true };
  });

  ipcMain.handle("scripts:rename-path", async (_, payload) => {
    const { absTarget } = resolveInScriptsRoot(payload.relativePath);
    const currentStat = await fsp.stat(absTarget);
    const parentAbs = path.dirname(absTarget);
    const nextName = String(payload.newName || "").trim();

    if (!nextName) {
      const err = new Error("名称不能为空");
      err.code = "INVALID_NAME";
      throw err;
    }
    if (nextName.includes("/") || nextName.includes("\\") || nextName === "." || nextName === "..") {
      const err = new Error("名称不合法");
      err.code = "INVALID_NAME";
      throw err;
    }

    const nextAbs = path.join(parentAbs, nextName);
    const { root } = resolveInScriptsRoot("");
    if (nextAbs !== root && !isWithin(root, nextAbs)) {
      const err = new Error("重命名路径超出脚本目录");
      err.code = "INVALID_PATH";
      throw err;
    }

    const exists = await fsp.stat(nextAbs).catch(() => null);
    if (exists) {
      const err = new Error("同级已存在同名文件或文件夹");
      err.code = "NAME_EXISTS";
      throw err;
    }

    if (path.basename(absTarget) === nextName) {
      return { ok: true, newRelativePath: payload.relativePath };
    }

    await fsp.rename(absTarget, nextAbs);

    const oldRel = normalizeRel(path.relative(root, absTarget));
    const newRel = normalizeRel(path.relative(root, nextAbs));

    if (currentStat.isFile() && appConfig.scriptConfigs[oldRel]) {
      appConfig.scriptConfigs[newRel] = appConfig.scriptConfigs[oldRel];
      delete appConfig.scriptConfigs[oldRel];
    }
    if (appConfig.runtimeRecords[oldRel]) {
      appConfig.runtimeRecords[newRel] = appConfig.runtimeRecords[oldRel];
      delete appConfig.runtimeRecords[oldRel];
    }
    await saveConfig();

    return { ok: true, newRelativePath: newRel };
  });

  ipcMain.handle("scripts:open-path", async (_, relativePath) => {
    const { absTarget } = resolveInScriptsRoot(relativePath);
    shell.showItemInFolder(absTarget);
    return { ok: true };
  });

  ipcMain.handle("script:get-config", async (_, relativePath) => {
    const current = appConfig.scriptConfigs[relativePath] || { interpreterPath: "", args: "" };
    if (current.interpreterPath && !current.interpreterDisplay) {
      const detected = await detectInterpreterInfo(current.interpreterPath);
      appConfig.scriptConfigs[relativePath] = {
        ...current,
        ...detected,
      };
      await saveConfig();
      return appConfig.scriptConfigs[relativePath];
    }
    return current;
  });

  ipcMain.handle("script:set-config", async (_, payload) => {
    const interpreterPath = payload.interpreterPath || "";
    let detected = {
      interpreterKind: "unknown",
      interpreterName: "Unknown",
      interpreterVersion: "",
      interpreterDisplay: "Unknown",
    };

    if (interpreterPath) {
      detected = await detectInterpreterInfo(interpreterPath);
    }

    appConfig.scriptConfigs[payload.relativePath] = {
      interpreterPath,
      args: payload.args || "",
      ...detected,
    };
    await saveConfig();
    return { ok: true, config: appConfig.scriptConfigs[payload.relativePath] };
  });

  ipcMain.handle("script:run", async (_, relativePath) => {
    if (runningScripts.has(relativePath)) {
      const err = new Error("该脚本正在运行中");
      err.code = "SCRIPT_RUNNING";
      throw err;
    }

    const { absTarget } = resolveInScriptsRoot(relativePath);
    const stat = await fsp.stat(absTarget);
    if (!stat.isFile()) {
      const err = new Error("只能运行脚本文件");
      err.code = "NOT_FILE";
      throw err;
    }

    const scriptCfg = appConfig.scriptConfigs[relativePath] || { interpreterPath: "", args: "" };
    if (!scriptCfg.interpreterPath) {
      const err = new Error("请先配置解释器绝对路径");
      err.code = "NO_INTERPRETER";
      throw err;
    }

    const interpreterAbs = path.resolve(scriptCfg.interpreterPath);
    const interpreterStat = await fsp.stat(interpreterAbs).catch(() => null);
    if (!interpreterStat || !interpreterStat.isFile()) {
      const err = new Error("解释器路径无效");
      err.code = "INVALID_INTERPRETER";
      throw err;
    }

    const args = [absTarget, ...parseArgs(scriptCfg.args)];
    const logsRoot = appConfig.logsRoot ? path.resolve(appConfig.logsRoot) : path.join(app.getPath("userData"), "logs");
    await ensureDir(logsRoot);

    const ext = path.extname(absTarget);
    const baseName = path.basename(absTarget, ext);
    const logFile = `${formatTime()}__${sanitizeName(relativePath)}__${sanitizeName(baseName)}.log`;
    const logPath = path.join(logsRoot, logFile);

    const startAt = Date.now();
    const header = [
      `startAt: ${new Date(startAt).toISOString()}`,
      `script: ${absTarget}`,
      `interpreter: ${interpreterAbs}`,
      `args: ${JSON.stringify(args.slice(1))}`,
      `result: running`,
      "",
    ].join("\n");

    await fsp.writeFile(logPath, header, "utf8");

    return await new Promise((resolve, reject) => {
      const stream = fs.createWriteStream(logPath, { flags: "a" });
      const child = spawn(interpreterAbs, args, {
        windowsHide: true,
      });

      runningScripts.set(relativePath, child.pid || -1);

      child.stdout.on("data", (chunk) => {
        stream.write(chunk);
      });

      child.stderr.on("data", (chunk) => {
        stream.write(chunk);
      });

      child.on("error", async (error) => {
        runningScripts.delete(relativePath);
        stream.write(`\nresult: error\n`);
        stream.end();

        const finishedAt = Date.now();
        const runtime = {
          lastRunAt: finishedAt,
          lastStatus: "error",
          lastExitCode: -1,
          lastDurationMs: finishedAt - startAt,
          lastLogPath: logPath,
          lastError: error.message,
        };
        appConfig.runtimeRecords[relativePath] = runtime;
        await saveConfig();

        await appendRunHistory({
          relativePath,
          ...runtime,
        });

        const runError = new Error(`运行失败，日志位置：${logPath}，错误：${error.message}`);
        runError.code = "RUN_ERROR";
        runError.logPath = logPath;
        reject(runError);
      });

      child.on("close", async (code) => {
        runningScripts.delete(relativePath);
        stream.write(`\nresult: ${code === 0 ? "success" : "failed"}\n`);
        stream.end();

        const finishedAt = Date.now();
        const success = code === 0;
        const runtime = {
          lastRunAt: finishedAt,
          lastStatus: success ? "success" : "failed",
          lastExitCode: code,
          lastDurationMs: finishedAt - startAt,
          lastLogPath: logPath,
          lastError: "",
        };
        appConfig.runtimeRecords[relativePath] = runtime;
        await saveConfig();

        await appendRunHistory({
          relativePath,
          ...runtime,
        });

        resolve({
          success,
          exitCode: code,
          logPath,
          runAt: finishedAt,
          durationMs: finishedAt - startAt,
        });
      });
    });
  });
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1380,
    height: 900,
    minWidth: 1080,
    minHeight: 680,
    backgroundColor: "#eef2ff",
    webPreferences: {
      preload: path.join(__dirname, "preload.cjs"),
      contextIsolation: true,
      nodeIntegration: false,
    },
  });

  if (isDev) {
    mainWindow.loadURL(DEV_URL);
  } else {
    mainWindow.loadFile(path.join(__dirname, "..", "dist", "index.html"));
  }

  mainWindow.on("minimize", (event) => {
    event.preventDefault();
    mainWindow.hide();
  });

  mainWindow.on("close", (event) => {
    if (!isQuitting) {
      event.preventDefault();
      mainWindow.hide();
    }
  });
}

app.whenReady().then(async () => {
  await loadConfig();
  registerIpc();
  createWindow();
  createTray();

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    } else if (mainWindow) {
      mainWindow.show();
      mainWindow.focus();
    }
  });
});

app.on("before-quit", () => {
  isQuitting = true;
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});
