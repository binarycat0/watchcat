import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { api } from '../api';
import { ActionButton, Empty, Pill, Time, useToast } from '../components';
import { ago, human, isFinished, prRef, prStatus } from '../format';
import type { AgentSession, CIRun, PRDetail, RuleInfo, Snapshot } from '../types';
import { TaskTable } from './Tasks';

export function PRPage() {
  const id = Number(useParams().id);
  const pr = useQuery({ queryKey: ['pr', id], queryFn: () => api.pr(id) });
  const tasks = useQuery({ queryKey: ['tasks', 'pr', id], queryFn: () => api.tasks({ pr: id, limit: 20 }) });
  const rules = useQuery({ queryKey: ['rules', id], queryFn: () => api.rules(id) });
  const runs = useQuery({ queryKey: ['ci-runs', id], queryFn: () => api.ciRuns(id) });
  const navigate = useNavigate();

  if (pr.isError) return <Empty>{(pr.error as Error).message}</Empty>;
  if (!pr.data) return null;
  const p = pr.data;
  const s = p.snapshot;

  return (
    <>
      <div className="header-row">
        <div>
          <div className="muted small"><Link to="/">Pull requests</Link> /</div>
          <h1>{s?.title || prRef(p)}</h1>
          <div>
            <a href={p.url} target="_blank" rel="noopener">{prRef(p)}</a> · <Pill status={prStatus(p)} />{' '}
            <span className="muted small">checked {ago(p.last_checked)}{p.paused ? '' : `, next ${ago(p.next_check)}`}</span>
          </div>
          <div className="muted small" style={{ marginTop: 4 }}>{p.summary}</div>
        </div>
        <div className="btns">
          <ActionButton fn={() => api.check(id)} ok="Check queued">Check now</ActionButton>
          {(s?.failed_checks?.length ?? 0) > 0 && <ActionButton fn={() => api.retrigger(id)} ok="Retrigger queued; see Tasks below">Retrigger failed</ActionButton>}
          {!isFinished(p) && (
            <ActionButton fn={() => api.setPaused(id, !p.paused)} ok={p.paused ? 'Resumed' : 'Paused'}>{p.paused ? 'Resume' : 'Pause'}</ActionButton>
          )}
          <ActionButton className="danger" confirm={`Stop watching ${prRef(p)}?`}
            fn={async () => { await api.removePR(id); navigate('/'); }} ok="Removed">Remove</ActionButton>
        </div>
      </div>

      <h2>Agent session</h2>
      <AgentSection p={p} rules={rules.data} />

      <h2>Current state</h2>
      {s ? <Facts s={s} /> : <Empty>Not checked yet.</Empty>}

      {!!s?.failed_checks?.length && (<>
        <h2>Failed checks</h2>
        <ul className="list">{s.failed_checks.map((c) => <li key={c.name}>{c.url ? <a href={c.url} target="_blank" rel="noopener">{c.name}</a> : c.name}</li>)}</ul>
      </>)}
      {!!s?.pending_checks?.length && (<>
        <h2>Pending checks</h2>
        <ul className="list">{s.pending_checks.map((n) => <li key={n}>{n}</li>)}</ul>
      </>)}

      {!!runs.data?.length && <CIRuns runs={runs.data} head={s?.head_sha} />}
      {rules.data && <Rules rules={rules.data} />}

      <h2>History</h2>
      {p.events.length ? (
        <ul className="list">{p.events.map((e) => (
          <li key={e.id}>
            <span className="when"><Time ts={e.created_at}>{ago(e.created_at)}</Time></span>
            <span className="kind"><Pill status={e.kind === 'ci_failed' || e.kind === 'conflict' ? 'error' : 'unknown'} label={e.kind} /></span>
            <span>{e.message}</span>
          </li>
        ))}</ul>
      ) : <Empty>No history yet.</Empty>}

      <h2>Tasks</h2>
      {tasks.data && <TaskTable tasks={tasks.data} />}
    </>
  );
}

function Facts({ s }: { s: Snapshot }) {
  const review: Record<string, string> = { APPROVED: 'approved', CHANGES_REQUESTED: 'changes requested', REVIEW_REQUIRED: 'review required' };
  const facts: [string, string][] = [
    ['CI', s.ci + (s.pending_checks?.length ? ` · ${s.pending_checks.length} pending` : '')],
    ['Mergeable', (s.mergeable || '').toLowerCase()],
    ['Review', review[s.review_decision] || 'none'],
    ['Unresolved threads', String(s.unresolved_threads)],
    ['Head', (s.head_sha || '').slice(0, 7)],
    ['State', (s.state || '').toLowerCase() + (s.draft ? ', draft' : '')],
  ];
  return <div className="grid">{facts.map(([k, v]) => <div className="fact" key={k}><div className="k">{k}</div><div className="v">{v}</div></div>)}</div>;
}

/** Sessions that mention this PR's repo or number first. */
function sortSessions(list: AgentSession[], p: PRDetail) {
  const score = (s: AgentSession) => {
    const text = `${s.title} ${s.directory}`.toLowerCase();
    return (text.includes(p.repo.toLowerCase()) ? 2 : 0) + (text.includes(String(p.number)) ? 1 : 0);
  };
  return [...list].sort((a, b) => score(b) - score(a));
}

function AgentSection({ p, rules }: { p: PRDetail; rules?: RuleInfo[] }) {
  // Listing shells out to opencode, so cache it for 30s and don't poll.
  const sessions = useQuery({ queryKey: ['agent-sessions'], queryFn: api.agentSessions, staleTime: 30000, refetchInterval: false, retry: false });
  const [session, setSession] = useState('');
  const toast = useToast();
  const qc = useQueryClient();
  const bind = useMutation({
    mutationFn: (s: string) => api.bindAgent(p.id, s),
    onSuccess: (_, s) => { setSession(''); toast(s ? 'Linked to ' + s : 'Unlinked'); },
    onError: (e: Error) => toast(e.message, true),
    onSettled: () => qc.invalidateQueries(),
  });
  const s = p.snapshot;
  const mode = (rule: string) => rules?.find((r) => r.name === rule)?.mode || 'off';

  return (<>
    {p.agent_session && (
      <p>Prompts go to opencode session <span className="mono">{p.agent_session}</span> in <span className="mono">{p.agent_worktree || '?'}</span></p>
    )}
    <form className="toolbar" onSubmit={(e) => { e.preventDefault(); if (session.trim()) bind.mutate(session.trim()); }}>
      <input list="sessions" value={session} onChange={(e) => setSession(e.target.value)} autoComplete="off" aria-label="opencode session"
        placeholder={sessions.isError ? 'opencode session ID (ses_...)' : 'Pick or type an opencode session'} />
      <datalist id="sessions">
        {sortSessions(sessions.data ?? [], p).map((x) => <option key={x.id} value={x.id}>{`${x.title || '(untitled)'} · ${x.directory}`}</option>)}
      </datalist>
      <button disabled={bind.isPending}>{p.agent_session ? 'Change' : 'Link'}</button>
      {p.agent_session && <button type="button" className="danger" onClick={() => bind.mutate('')}>Unlink</button>}
    </form>
    {sessions.isError && <p className="muted small">Couldn't list opencode sessions: {(sessions.error as Error).message}</p>}
    {p.agent_session && s && (
      <div className="toolbar">
        {s.ci === 'failure' && <PromptButton id={p.id} rule="ci_failed" what="CI failure" mode={mode('ci_failed')} />}
        {s.unresolved_threads > 0 && <PromptButton id={p.id} rule="review_threads" what="review threads" mode={mode('review_threads')} />}
      </div>
    )}
  </>);
}

/** Manual prompts follow the matching rule's mode, and the label says so. */
function PromptButton({ id, rule, what, mode }: { id: number; rule: string; what: string; mode: string }) {
  return (
    <ActionButton fn={() => api.promptAgent(id, rule)}
      ok={(r) => (r as { dry_run: boolean })?.dry_run
        ? 'Dry-run: prompt built, not sent (see Tasks below). Switch the rule to "on" in Settings to send.'
        : 'Prompt queued; see Tasks below for the result'}>
      {mode === 'on' ? `Send ${what} to agent` : `Preview ${what} prompt (${mode}, not sent)`}
    </ActionButton>
  );
}

function CIRuns({ runs, head }: { runs: CIRun[]; head?: string }) {
  const bySha = new Map<string, CIRun[]>();
  for (const r of runs) bySha.set(r.head_sha, [...(bySha.get(r.head_sha) ?? []), r]);
  const duration = (r: CIRun) => r.completed_at
    ? human((new Date(r.completed_at).getTime() - new Date(r.started_at).getTime()) / 1000)
    : `running for ${human((Date.now() - new Date(r.started_at).getTime()) / 1000)}`;
  return (<>
    <h2>CI runs</h2>
    <table>
      <thead><tr><th>Job</th><th>Result</th><th className="hide-sm">Started</th><th>Duration</th></tr></thead>
      {[...bySha.entries()].map(([sha, rs]) => (
        <tbody key={sha}>
          <tr className="health-head"><td colSpan={4}><span className="mono">{sha.slice(0, 7)}</span>{' '}
            {sha === head && <span className="muted small">current commit</span>}</td></tr>
          {rs.map((r) => (
            <tr key={r.id}>
              <td>{r.url ? <a href={r.url} target="_blank" rel="noopener">{r.name}</a> : r.name}</td>
              <td><Pill status={r.result || 'running'} /></td>
              <td className="hide-sm"><Time ts={r.started_at}>{ago(r.started_at)}</Time></td>
              <td>{duration(r)}</td>
            </tr>
          ))}
        </tbody>
      ))}
    </table>
  </>);
}

function Rules({ rules }: { rules: RuleInfo[] }) {
  return (<>
    <h2>Workflow rules</h2>
    <table>
      <thead><tr><th>Rule</th><th>Mode</th><th>Matches now</th><th className="hide-sm">Last fired</th></tr></thead>
      <tbody>{rules.map((r) => (
        <tr key={r.name}>
          <td><span className="mono">{r.name}</span><div className="muted small">{r.action}</div></td>
          <td><Pill status={r.mode || 'off'} /></td>
          <td>{r.matches ? <Pill status="warn" label="yes" /> : <span className="muted">no</span>}</td>
          <td className="hide-sm">{r.last_run
            ? <><Time ts={r.last_run.created_at}>{ago(r.last_run.created_at)}</Time> <span className="muted small">({r.last_run.mode})</span></>
            : <span className="muted">never</span>}</td>
        </tr>
      ))}</tbody>
    </table>
    <p className="muted small">A rule fires once per PR state (new commit, new failures or threads). Change modes on the <Link to="/config">Config</Link> page.</p>
  </>);
}
