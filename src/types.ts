export type NodeKind = "file" | "folder";

export interface ScriptNode {
  kind: NodeKind;
  name: string;
  relativePath: string;
  absolutePath: string;
  createdAtMs?: number;
  updatedAtMs?: number;
  children?: ScriptNode[];
}

export interface ScriptRuntimeRecord {
  lastRunAt: number;
  lastStatus: "success" | "failed" | "error";
  lastExitCode: number | null;
  lastDurationMs: number;
  lastLogPath: string;
  lastError: string;
}

export interface ScriptConfig {
  interpreterPath: string;
  args: string;
  interpreterKind?: string;
  interpreterName?: string;
  interpreterVersion?: string;
  interpreterDisplay?: string;
}

export interface SettingsData {
  scriptsRoot: string;
  logsRoot: string;
}

export interface StartupInfo {
  hasScriptsRoot: boolean;
  scriptsRootValid: boolean;
  message: string;
}

export interface TreeResult {
  scriptsRoot: string;
  nodes: ScriptNode[];
  runtimeRecords: Record<string, ScriptRuntimeRecord>;
  running: string[];
  scriptConfigs: Record<string, ScriptConfig>;
}

export interface RunResult {
  success: boolean;
  exitCode: number;
  logPath: string;
  runAt: number;
  durationMs: number;
}

export interface ToastItem {
  id: number;
  type: "success" | "error" | "warning" | "info";
  message: string;
}
