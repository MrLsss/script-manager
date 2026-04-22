# Script Manager

跨平台脚本管理器（Windows / macOS / Linux），基于 Electron + Vue 3 + Vite + TypeScript。

## 功能概览

### 1. 脚本管理
- 支持配置一个脚本根目录，递归展示目录树。
- 左侧同时显示：
  - 根目录下的脚本文件（与文件夹同级）
  - 多级子文件夹与其内脚本
- 支持创建：
  - 脚本：`未命名脚本1/2/...`
  - 文件夹：`未命名文件夹1/2/...`
- 支持重命名（树内联编辑，`Enter` 确认，`Esc` 取消）。
- 支持删除文件/文件夹、打开本地路径。
- 支持手动刷新脚本树（并在窗口重新聚焦时自动刷新）。

### 2. 脚本配置
- 每个脚本独立配置：
  - 解释器绝对路径（可手动输入或文件选择）
  - 运行参数（按命令行参数拼接）
- 编辑区按钮和右键菜单统一为“脚本配置”。

### 3. 脚本运行
- 运行命令：`<interpreter> <scriptPath> <args...>`
- 同一脚本运行中会阻塞重复点击，避免并发重复执行。
- 运行结束后自动更新：
  - 上次运行时间
  - 运行状态（成功/失败/异常）
- 运行失败时会提示日志路径。

### 4. 日志记录
- 每次运行生成一个独立日志文件。
- 日志内容包含：
  - `startAt`
  - `script`
  - `interpreter`
  - `args`
  - `result`（`running` / `success` / `failed` / `error`）
- stdout/stderr 会写入同一日志文件。

### 5. 设置管理
- 可配置：
  - 脚本根目录
  - 日志目录
- 未配置脚本目录时：
  - 进入应用会有提示
  - 点击新增会引导到设置页

### 6. 托盘/菜单栏
- 最小化后隐藏到托盘（系统菜单栏区域）。
- 托盘菜单支持：
  - 显示主窗口
  - 退出

## 技术栈
- Electron
- Vue 3
- Vite
- TypeScript
- Vue Router
- Tailwind CSS v4
- CodeMirror 6（脚本编辑高亮）

## 本地开发

```bash
pnpm install
pnpm run dev
```

默认开发端口：`127.0.0.1:5174`

## 生产构建

```bash
pnpm run build
```

## 关键目录

```text
electron/               # Electron 主进程 & preload
src/views/              # Home / Settings 页面
src/components/         # 脚本树、编辑器、toast 等组件
src/stores/             # 应用状态管理
src/services/           # 渲染层 API 封装
build/icons/            # 安装包图标产物（Windows/macOS/Linux）
```

## 配置与数据落盘

应用会在 Electron `userData` 目录下维护配置和运行记录（JSON 文件），并在配置的日志目录下写入运行日志。

## 生成安装包图标

```bash
pnpm run icons:generate
```

生成后可直接用于三平台打包：

- Windows（安装包/EXE）：`build/icons/icon.ico`
- macOS（App/DMG）：`build/icons/icon.icns`
- Linux（桌面图标）：`build/icons/512x512.png`
