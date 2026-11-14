// Mirrors the Go JSON in internal/core, internal/api, internal/health, internal/agent.

export interface Check { name: string; url?: string }

export interface Snapshot {
  title: string;
  state: 'OPEN' | 'CLOSED' | 'MERGED' | string;
  draft: boolean;
  mergeable: string;
  review_decision: string;
  head_sha: string;
  ci: 'none' | 'pending' | 'success' | 'failure';
  failed_checks?: Check[];
  pending_checks?: string[];
  unresolved_threads: number;
  base_ref?: string;
  auto_merge?: boolean;
  opened_at?: string;
  merged_at?: string;
  closed_at?: string;
}

export interface PR {
  id: number;
  host: string;
  owner: string;
  repo: string;
  number: number;
  paused: boolean;
  status: string;
  summary: string;
  created_at: string;
  last_checked?: string;
  next_check: string;
  snapshot?: Snapshot;
  agent_session?: string;
  agent_worktree?: string;
}

export interface Event { id: number; pr_id: number; kind: string; message: string; created_at: string }

export interface PRDetail extends PR { url: string; events: Event[] }

export interface Task {
  id: number;
  kind: string;
  pr_id?: number;
  payload: unknown;
  status: 'queued' | 'running' | 'done' | 'failed';
  attempts: number;
  max_attempts: number;
  last_error?: string;
  result?: string;
  run_at: string;
  created_at: string;
  updated_at: string;
}

export interface Status {
  version: string;
  pid: number;
  started_at: string;
  uptime: string;
  db_path: string;
  prs: number;
  running: Task[];
  slack: boolean;
}

export type HealthStatus = 'ok' | 'warn' | 'fail' | 'skip';
export interface HealthDetail { label: string; value: string; status?: HealthStatus }
export interface HealthResult {
  name: string; title: string; required: boolean; status: HealthStatus;
  summary: string; version?: string; details?: HealthDetail[];
}
export interface HealthReport { status: HealthStatus; checked_at: string; results: HealthResult[] }

export interface RuleRun { rule: string; state_key: string; mode: string; created_at: string }
export interface RuleInfo { name: string; action: string; mode?: string; matches: boolean; last_run?: RuleRun }

export interface Settings {
  workflows: Record<string, string>;
  merge_method: string;
  poll_interval: string;
  notify_on: string[];
  slack_user_id: string;
  slack_channel: string;
}
export interface SettingsResponse {
  settings: Settings;
  rules: RuleInfo[];
  modes: string[];
  merge_methods: string[];
  change_kinds: string[];
  config_path: string;
}

export interface AgentSession { id: string; title: string; directory: string; archived: boolean }

export interface CIRun {
  id: number; pr_id: number; head_sha: string; name: string; kind: string; run_key: string;
  url?: string; result: string; started_at: string; completed_at?: string;
}

export interface Stats { count: number; p50: number; p95: number; max: number }
export interface JobStats { name: string; runs: number; running: number; failures: number; duration: Stats }
export interface ReportRow {
  id: number; ref: string; url: string; title: string; state: string;
  opened_at?: string; merged_at?: string; time_to_merge?: number; ci_failures: number;
}
export interface Report {
  generated_at: string; since?: string;
  watched: number; open: number; merged: number; closed: number; paused: number;
  time_to_merge: Stats; ci_failures: Stats; missing_times: number;
  prs: ReportRow[]; pipeline_time: Stats; jobs: JobStats[];
}

export interface TemplateVar { name: string; desc: string }
export interface TemplateInfo {
  name: string; title: string; group: 'notify' | 'agent'; description: string;
  vars: TemplateVar[]; uses?: string[];
  text: string; default: string; custom: boolean; path?: string; error?: string;
}
