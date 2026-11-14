import { useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom';
import { api } from '../api';
import { ActionButton, Empty, PagerBar, Pill, SettingsLink, Tabs, Time, useServerPage, useToast } from '../components';
import { ago, human, prRef, watchState } from '../format';
import type { AgentSession, CIRun, PRDetail, RuleInfo, Snapshot } from '../types';
import { TaskTable } from './Tasks';
import { WatchActions } from '../WatchActions';

type Tab = 'history' | 'tasks' | 'automations';

export function PRPage() {
  const id = Number(useParams().id);
  const pr = useQuery({ queryKey: ['pr', id], queryFn: () => api.pr(id) });
  const rules = useQuery({ queryKey: ['rules', id], queryFn: () => api.rules(id) });
  const runs = useQuery({ queryKey: ['ci-runs', id], queryFn: () => api.ciRuns(id) });
  const [params, setParams] = useSearchParams();
  const tab = (['history', 'tasks', 'automations'].includes(params.get('tab') ?? '') ? params.get('tab') : 'history') as Tab;
  const navigate = useNavigate();

  if (pr.isError) return <Empty>{(pr.error as Error).message}</Empty>;
  if (!pr.data) return null;
  const p = pr.data;
  const s = p.snapshot;
  const ws = watchState(p);

  return (
    <>
      <div className="muted small"><Link to="/">Pull requests</Link> /</div>
      <div className="pr-head">
        <div className="pr-title">
          <h1>{s?.title || prRef(p)}</h1>
          <Pill status={ws} />
          <span className="muted small">
            checked <Time ts={p.last_checked}>{ago(p.last_checked)}</Time>
            {ws === 'watching' && <>, next <Time ts={p.next_check}>{ago(p.next_check)}</Time></>}
          </span>
        </div>
        <WatchActions p={p} variant="menu" onRemoved={() => navigate('/')} />
      </div>
      <div className="pr-sub">
        <StatePill s={s} fallback={p.status} />
        <a href={p.url} target="_blank" rel="noopener">{prRef(p)}</a>
        {s && <CIPill ci={s.ci} />}
      </div>

      <h2>Current state</h2>
      {s ? <Facts s={s} /> : <Empty>Not checked yet.</Empty>}

      {!!s?.failed_checks?.length && (<>
        <div className="section-head">
          <h2>Failed checks</h2>
          <ActionButton fn={() => api.retrigger(id)} ok="Retrigger queued; see the Tasks tab">Retrigger failed</ActionButton>
        </div>
        <ul className="list">{s.failed_checks.map((c) => <li key={c.name}>{c.url ? <a href={c.url} target="_blank" rel="noopener">{c.name}</a> : c.name}</li>)}</ul>
      </>)}
      {!!s?.pending_checks?.length && (<>
        <h2>Pending checks</h2>
        <ul className="list">{s.pending_checks.map((n) => <li key={n}>{n}</li>)}</ul>
      </>)}
      {!!runs.data?.length && <CIRuns runs={runs.data} head={s?.head_sha} />}

      <div className="tab-block">
        <Tabs<Tab>
          active={tab}
          onChange={(t) => setParams(t === 'history' ? {} : { tab: t }, { replace: true })}
          tabs={[
            { key: 'history', label: 'History' },
            { key: 'tasks', label: 'Tasks' },
            { key: 'automations', label: <>Automations{p.agent_session ? <span className="tab-note"> · agent linked</span> : null}</> },
          ]}
        />
        <div className="tab-panel" role="tabpanel">
          {tab === 'history' && <History id={id} />}
          {tab === 'tasks' && <PRTasks id={id} />}
          {tab === 'automations' && (<>
            <h3>AI agent session</h3>
            <AgentSection p={p} rules={rules.data} />
            {rules.data && <Rules rules={rules.data} />}
          </>)}
        </div>
      </div>
    </>
  );
}

const PAGE_SIZE = 20;

function History({ id }: { id: number }) {
  const { rows, pager } = useServerPage(['events', id], (limit, offset) => api.events(id, limit, offset), PAGE_SIZE);
  if (!rows) return null;
  if (!rows.length) return <Empty>No history yet.</Empty>;
  return (<>
    <table>
      <thead><tr><th>When</th><th>Event</th><th>Details</th></tr></thead>
      <tbody>{rows.map((e) => (
        <tr key={e.id}>
          <td className="nowrap"><Time ts={e.created_at}>{ago(e.created_at)}</Time></td>
          <td><Pill status={e.kind === 'ci_failed' || e.kind === 'conflict' ? 'error' : 'unknown'} label={e.kind} /></td>
          <td>{e.message}</td>
        </tr>
      ))}</tbody>
    </table>
    <PagerBar {...pager} />
  </>);
}

function PRTasks({ id }: { id: number }) {
  const { rows, pager } = useServerPage(['tasks', 'pr', id], (limit, offset) => api.tasks({ pr: id, limit, offset }), PAGE_SIZE);
  if (!rows) return null;
  return (<>
    <TaskTable tasks={rows} />
    <PagerBar {...pager} />
  </>);
}

/** The PR's own state: open, draft, merged or closed. */
function StatePill({ s, fallback }: { s?: Snapshot; fallback: string }) {
  if (!s) return <Pill status={fallback} />;
  if (s.state === 'OPEN') return s.draft ? <Pill status="idle" tone="idle" label="draft" /> : <Pill status="open" />;
  return <Pill status={s.state.toLowerCase()} />;
}

function CIPill({ ci }: { ci: Snapshot['ci'] }) {
  const map: Record<string, [string, string]> = {
    success: ['success', 'CI passed'], failure: ['failure', 'CI failed'], pending: ['running', 'CI running'], none: ['skipped', 'no CI'],
  };
  const [status, label] = map[ci] ?? ['unknown', 'CI ' + ci];
  return <Pill status={status} label={label} />;
}

/** What the title rows don't already say: review, merge readiness, commits, dates. */
function Facts({ s }: { s: Snapshot }) {
  const review: Record<string, string> = { APPROVED: 'approved', CHANGES_REQUESTED: 'changes requested', REVIEW_REQUIRED: 'review required' };
  const mergeable: Record<string, string> = { MERGEABLE: 'yes', CONFLICTING: 'no, merge conflict', UNKNOWN: 'unknown' };
  const rows: [string, ReactNode][] = [
    ['Review', review[s.review_decision] || 'none'],
    ['Unresolved threads', s.unresolved_threads ? <span className="bad">{s.unresolved_threads}</span> : '0'],
    ['Mergeable', s.mergeable === 'CONFLICTING' ? <span className="bad">{mergeable.CONFLICTING}</span> : mergeable[s.mergeable] || (s.mergeable || '–').toLowerCase()],
    ['Pending checks', String(s.pending_checks?.length ?? 0)],
    ['Head commit', <span className="mono">{(s.head_sha || '').slice(0, 7) || '–'}</span>],
  ];
  if (s.base_ref) rows.push(['Base branch', <span className="mono">{s.base_ref}</span>]);
  if (s.auto_merge !== undefined) rows.push(['Auto-merge', s.auto_merge ? 'on' : 'off']);
  if (s.opened_at) rows.push(['Opened', <Time ts={s.opened_at}>{ago(s.opened_at)}</Time>]);
  if (s.merged_at) rows.push(['Merged', <Time ts={s.merged_at}>{ago(s.merged_at)}</Time>]);
  else if (s.closed_at) rows.push(['Closed', <Time ts={s.closed_at}>{ago(s.closed_at)}</Time>]);
  return (
    <div className="table-scroll">
      <table className="state-row">
        <thead><tr>{rows.map(([k]) => <th key={k}>{k}</th>)}</tr></thead>
        <tbody><tr>{rows.map(([k, v]) => <td key={k}>{v}</td>)}</tr></tbody>
      </table>
    </div>
  );
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
        ? 'Dry-run: prompt built, not sent (see the Tasks tab). Switch the rule to "on" in Config › Workflow rules to send.'
        : 'Prompt queued; see the Tasks tab for the result'}>
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
    <h3>Workflow rules</h3>
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
    <p className="muted small">A rule fires once per PR state (new commit, new failures or threads). Change modes on the <SettingsLink page="config" tab="rules" /> page.</p>
  </>);
}
