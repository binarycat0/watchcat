// Thin client for the daemon's /api/v1. Errors carry the server's message.
import type {
  AgentSession, CIRun, HealthReport, PR, PRDetail, Report, RuleInfo, Settings, SettingsResponse, Status, Task,
} from './types';

export class ApiError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
  }
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch('/api/v1' + path, {
    method,
    headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text(); // 202/204 may have no body
  const data = text ? JSON.parse(text) : null;
  if (!res.ok) throw new ApiError(data?.error || res.statusText, res.status);
  return data as T;
}

export const api = {
  status: () => call<Status>('GET', '/status'),
  health: (refresh = false) => call<HealthReport>('GET', '/health' + (refresh ? '?refresh=1' : '')),
  prs: () => call<PR[]>('GET', '/prs'),
  pr: (id: number) => call<PRDetail>('GET', `/prs/${id}`),
  addPR: (ref: string) => call<PR>('POST', '/prs', { ref }),
  removePR: (id: number) => call<null>('DELETE', `/prs/${id}`),
  check: (id: number) => call<null>('POST', `/prs/${id}/check`),
  retrigger: (id: number) => call<null>('POST', `/prs/${id}/retrigger`),
  setPaused: (id: number, paused: boolean) => call<null>('POST', `/prs/${id}/${paused ? 'pause' : 'resume'}`),
  rules: (id: number) => call<RuleInfo[]>('GET', `/prs/${id}/rules`),
  ciRuns: (id: number) => call<CIRun[]>('GET', `/prs/${id}/ci-runs`),
  bindAgent: (id: number, session: string) => call<null>('PUT', `/prs/${id}/agent`, { session, worktree: '' }),
  promptAgent: (id: number, prompt: string) =>
    call<{ dry_run: boolean }>('POST', `/prs/${id}/agent/prompt`, { prompt }),
  agentSessions: () => call<AgentSession[]>('GET', '/agent/sessions'),
  tasks: (q: { status?: string; pr?: number; limit?: number }) => {
    const p = new URLSearchParams();
    if (q.status) p.set('status', q.status);
    if (q.pr) p.set('pr', String(q.pr));
    if (q.limit) p.set('limit', String(q.limit));
    return call<Task[]>('GET', '/tasks?' + p.toString());
  },
  stats: (days: number) => call<Report>('GET', '/stats' + (days ? `?days=${days}` : '')),
  settings: () => call<SettingsResponse>('GET', '/settings'),
  saveSettings: (s: Settings) => call<SettingsResponse>('PUT', '/settings', s),
  slackTest: () => call<null>('POST', '/slack/test'),
};
