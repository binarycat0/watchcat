import type { PR } from './types';

export function ago(ts?: string | null): string {
  if (!ts) return 'never';
  const s = Math.round((Date.now() - new Date(ts).getTime()) / 1000);
  if (s < 0) return 'in ' + dur(-s);
  if (s < 5) return 'just now';
  return dur(s) + ' ago';
}

function dur(s: number): string {
  if (s < 60) return s + 's';
  if (s < 3600) return Math.round(s / 60) + 'm';
  if (s < 86400) return Math.round(s / 3600) + 'h';
  return Math.round(s / 86400) + 'd';
}

/** Seconds as "3d 4h", "5h 12m" or "18m". */
export function human(secs: number): string {
  const m = Math.round(secs / 60), h = Math.floor(m / 60), d = Math.floor(h / 24);
  if (d > 0) return `${d}d ${h % 24}h`;
  if (h > 0) return `${h}h ${m % 60}m`;
  return `${m}m`;
}

export const fullTime = (ts?: string | null) => (ts ? new Date(ts).toLocaleString() : '');

const host = (p: PR) => p.host || 'github.com';
/** Same format as the CLI: github.com PRs stay short, others show their host. */
export const prRef = (p: PR) => `${host(p) === 'github.com' ? '' : host(p) + '/'}${p.owner}/${p.repo}#${p.number}`;
export const prURL = (p: PR) => `https://${host(p)}/${p.owner}/${p.repo}/pull/${p.number}`;

export const isFinished = (p: PR) => p.status === 'merged' || p.status === 'closed';
export const prStatus = (p: PR) => (p.paused && !isFinished(p) ? 'paused' : p.status);
export const needsAttention = (p: PR) =>
  !p.paused && ['ci_failed', 'conflict', 'changes_requested', 'unresolved_comments', 'error'].includes(p.status);
