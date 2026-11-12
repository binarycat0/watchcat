import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api } from '../api';
import { ActionButton, Empty, useToast } from '../components';
import type { Settings } from '../types';

export function SettingsPage() {
  // Settings change rarely; don't poll, so the form is never overwritten mid-edit.
  const data = useQuery({ queryKey: ['settings'], queryFn: api.settings, refetchInterval: false });
  const health = useQuery({ queryKey: ['health'], queryFn: () => api.health(), refetchInterval: 60000 });
  const [form, setForm] = useState<Settings | null>(null);
  const toast = useToast();
  const qc = useQueryClient();
  useEffect(() => { if (data.data && !form) setForm(data.data.settings); }, [data.data, form]);

  const save = useMutation({
    mutationFn: api.saveSettings,
    onSuccess: (r) => { setForm(r.settings); qc.setQueryData(['settings'], r); toast('Settings saved'); },
    onError: (e: Error) => toast(e.message, true),
    onSettled: () => qc.invalidateQueries({ predicate: (q) => q.queryKey[0] !== 'settings' }),
  });

  if (data.isError) return <Empty>{(data.error as Error).message}</Empty>;
  if (!data.data || !form) return null;
  const d = data.data;
  const set = (patch: Partial<Settings>) => setForm({ ...form, ...patch });
  const secrets = (health.data?.results.find((r) => r.name === 'setup')?.details ?? []).filter((x) => x.label.startsWith('secret '));

  return (
    <>
      <h1>Settings</h1>
      <p className="muted">Saved to <span className="mono">{d.config_path}</span> and applied right away; no restart needed.</p>
      <form onSubmit={(e) => { e.preventDefault(); save.mutate(form); }}>
        <h2>Workflow rules</h2>
        <table>
          <thead><tr><th>Rule</th><th>Mode</th></tr></thead>
          <tbody>{d.rules.map((r) => (
            <tr key={r.name}>
              <td><span className="mono">{r.name}</span><div className="muted small">{r.action}</div></td>
              <td><select aria-label={`${r.name} mode`} value={form.workflows[r.name] || 'off'}
                onChange={(e) => set({ workflows: { ...form.workflows, [r.name]: e.target.value } })}>
                {d.modes.map((m) => <option key={m}>{m}</option>)}
              </select></td>
            </tr>
          ))}</tbody>
        </table>
        <p className="muted small"><b>dry-run</b> builds the command or prompt and records it as the task's result without acting. <b>on</b> acts for real, including manual "send to agent" buttons.</p>

        <h2>Checks and merging</h2>
        <div className="form-grid">
          <label>Check every <input value={form.poll_interval} onChange={(e) => set({ poll_interval: e.target.value })} placeholder="5m" /></label>
          <label>Merge method <select value={form.merge_method} onChange={(e) => set({ merge_method: e.target.value })}>
            {d.merge_methods.map((m) => <option key={m}>{m}</option>)}
          </select></label>
          <label>Host for short refs (owner/repo#123) <input value={form.github_host} onChange={(e) => set({ github_host: e.target.value })} placeholder="github.com" /></label>
        </div>
        <p className="muted small">PR URLs and host/owner/repo#123 refs carry their own host. Watchdog only accepts hosts gh is signed in to; see <Link to="/health">Health</Link>.</p>

        <h2>Slack</h2>
        <div className="form-grid">
          <label>Your Slack user ID (DMs) <input value={form.slack_user_id} onChange={(e) => set({ slack_user_id: e.target.value })} placeholder="U0123456789" /></label>
          <label>Or a channel ID <input value={form.slack_channel} onChange={(e) => set({ slack_channel: e.target.value })} placeholder="C0123456789" /></label>
        </div>
        <fieldset>
          <legend className="muted small">Send a DM when a PR changes like this</legend>
          {d.change_kinds.map((k) => (
            <label key={k} className="check">
              <input type="checkbox" checked={form.notify_on.includes(k)}
                onChange={(e) => set({ notify_on: e.target.checked ? [...form.notify_on, k] : form.notify_on.filter((x) => x !== k) })} />
              {k.replace(/_/g, ' ')}
            </label>
          ))}
        </fieldset>
        <div className="toolbar">
          <button className="primary" disabled={save.isPending}>Save settings</button>
          <ActionButton fn={api.slackTest} ok="Test message sent">Send Slack test</ActionButton>
        </div>
      </form>

      <h2>Secrets</h2>
      <p className="muted small">Stored in the OS keychain, never in config.json or the browser. Set them from a terminal:{' '}
        <span className="mono">watchdog secret set slack-token</span>, then <span className="mono">watchdog service restart</span>.</p>
      {secrets.length ? (
        <ul className="list">{secrets.map((x) => (
          <li key={x.label}><span className="kind mono">{x.label.replace('secret ', '')}</span><span className={x.status === 'fail' ? 'bad' : ''}>{x.value}</span></li>
        ))}</ul>
      ) : <Empty>Secret status unavailable.</Empty>}
    </>
  );
}
