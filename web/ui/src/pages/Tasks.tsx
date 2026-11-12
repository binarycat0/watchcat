import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api } from '../api';
import { Empty, Pill, Time } from '../components';
import { ago, prRef } from '../format';
import type { PR, Task } from '../types';

export function TasksPage() {
  const [status, setStatus] = useState('');
  const tasks = useQuery({ queryKey: ['tasks', status], queryFn: () => api.tasks({ status, limit: 100 }) });
  const prs = useQuery({ queryKey: ['prs'], queryFn: api.prs });
  return (
    <>
      <h1>Tasks</h1>
      <p className="muted">Background work run by the daemon's workers: PR checks, Slack messages, retriggers, merges and agent prompts. Failed tasks retry with backoff.</p>
      <div className="toolbar">
        <label className="muted small">Status{' '}
          <select value={status} onChange={(e) => setStatus(e.target.value)}>
            {['', 'queued', 'running', 'done', 'failed'].map((s) => <option key={s} value={s}>{s || 'all'}</option>)}
          </select>
        </label>
      </div>
      {tasks.data && <TaskTable tasks={tasks.data} prs={prs.data} />}
    </>
  );
}

/** Tasks with their error or result; with prs given, adds a PR column. */
export function TaskTable({ tasks, prs }: { tasks: Task[]; prs?: PR[] }) {
  if (!tasks.length) return <Empty>No tasks.</Empty>;
  const byID = new Map((prs ?? []).map((p) => [p.id, p]));
  return (
    <table>
      <thead><tr><th>Task</th>{prs && <th>PR</th>}<th>Status</th><th className="hide-sm">Tries</th><th className="hide-sm">Updated</th><th>Error / result</th></tr></thead>
      <tbody>
        {tasks.map((t) => {
          const p = t.pr_id ? byID.get(t.pr_id) : undefined;
          const later = t.status === 'queued' && new Date(t.run_at).getTime() > Date.now();
          return (
            <tr key={t.id}>
              <td><span className="mono">{t.kind}</span> <span className="muted small">#{t.id}</span></td>
              {prs && <td>{p ? <Link to={`/pr/${p.id}`}>{prRef(p)}</Link> : t.pr_id ? '#' + t.pr_id : '–'}</td>}
              <td><Pill status={t.status} /></td>
              <td className="hide-sm">{t.attempts}/{t.max_attempts}</td>
              <td className="hide-sm"><Time ts={t.updated_at}>{later ? 'runs ' + ago(t.run_at) : ago(t.updated_at)}</Time></td>
              <td className="small">
                {t.last_error}
                {t.result && <details><summary>result</summary><pre>{t.result}</pre></details>}
              </td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
