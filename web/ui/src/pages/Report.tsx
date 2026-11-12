import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { api } from '../api';
import { Empty, Pill, Time } from '../components';
import { ago, human } from '../format';
import type { Stats } from '../types';

function Tile({ k, v, sub }: { k: string; v: string | number; sub?: string }) {
  return <div className="fact"><div className="k">{k}</div><div className="v big">{v}</div>{sub && <div className="muted small">{sub}</div>}</div>;
}

const durTile = (s: Stats, f: (s: Stats) => number) => (s.count ? human(f(s)) : '–');

export function ReportPage() {
  const [days, setDays] = useState(30);
  const report = useQuery({ queryKey: ['report', days], queryFn: () => api.report(days), refetchInterval: 30000 });
  const navigate = useNavigate();
  const r = report.data;
  const plural = (n: number, w: string) => `${n} ${w}${n === 1 ? '' : 's'}`;

  return (
    <>
      <div className="header-row">
        <div>
          <h1>Report</h1>
          <p className="muted">PR lifecycles for PRs open at any time in the period. CI failures and runs count what watchdog saw while watching.</p>
        </div>
        <label className="muted small">Period{' '}
          <select value={days} onChange={(e) => setDays(Number(e.target.value))}>
            <option value={7}>last 7 days</option><option value={30}>last 30 days</option>
            <option value={90}>last 90 days</option><option value={0}>all time</option>
          </select>
        </label>
      </div>
      {report.isError && <Empty>{(report.error as Error).message}</Empty>}
      {r && (<>
        <div className="grid">
          <Tile k="PRs watched" v={r.watched} sub={`${r.open} open · ${r.merged} merged · ${r.closed} closed`} />
          <Tile k="Time to merge, p50" v={durTile(r.time_to_merge, (s) => s.p50)} sub={r.time_to_merge.count ? `over ${plural(r.time_to_merge.count, 'merged PR')}` : 'no merged PRs'} />
          <Tile k="Time to merge, p95" v={durTile(r.time_to_merge, (s) => s.p95)} sub={r.time_to_merge.count ? `max ${human(r.time_to_merge.max)}` : undefined} />
          <Tile k="CI failures per PR, p50" v={r.ci_failures.count ? r.ci_failures.p50 : '–'} sub={r.ci_failures.count ? `over ${plural(r.ci_failures.count, 'PR')}` : undefined} />
          <Tile k="CI failures per PR, p95" v={r.ci_failures.count ? r.ci_failures.p95 : '–'} sub={r.ci_failures.count ? `max ${r.ci_failures.max}` : undefined} />
          <Tile k="Time to CI result, p50" v={durTile(r.pipeline_time, (s) => s.p50)} sub={r.pipeline_time.count ? `over ${plural(r.pipeline_time.count, 'commit')}` : 'first job start to last job end'} />
          <Tile k="Time to CI result, p95" v={durTile(r.pipeline_time, (s) => s.p95)} sub={r.pipeline_time.count ? `max ${human(r.pipeline_time.max)}` : undefined} />
        </div>
        {r.missing_times > 0 && <p className="muted small">{r.missing_times} merged PR(s) have no GitHub timestamps yet; they fill in on the next check.</p>}

        <h2>CI jobs</h2>
        {r.jobs.length ? (
          <table>
            <thead><tr><th>Job</th><th>Runs</th><th>Failed</th><th>p50</th><th>p95</th><th className="hide-sm">Max</th></tr></thead>
            <tbody>{r.jobs.map((j) => (
              <tr key={j.name}>
                <td>{j.name}{j.running > 0 && <span className="muted small"> ({j.running} running)</span>}</td>
                <td>{j.runs}</td><td>{j.failures}</td>
                <td>{durTile(j.duration, (s) => s.p50)}</td><td>{durTile(j.duration, (s) => s.p95)}</td>
                <td className="hide-sm">{durTile(j.duration, (s) => s.max)}</td>
              </tr>
            ))}</tbody>
          </table>
        ) : <Empty>No CI runs recorded in this period yet. They are collected on every check.</Empty>}

        <h2>PRs</h2>
        {r.prs.length ? (
          <table>
            <thead><tr><th>PR</th><th>State</th><th className="hide-sm">Opened</th><th>Time to merge</th><th>CI failures</th></tr></thead>
            <tbody>{[...r.prs]
              .sort((a, b) => (b.merged_at || b.opened_at || '').localeCompare(a.merged_at || a.opened_at || ''))
              .map((p) => (
                <tr key={p.id} className="click" onClick={(e) => { if (!(e.target as HTMLElement).closest('a')) navigate(`/pr/${p.id}`); }}>
                  <td><a href={p.url} target="_blank" rel="noopener">{p.ref}</a><span className="title">{p.title}</span></td>
                  <td><Pill status={p.state === 'open' ? 'ci_running' : p.state} label={p.state} /></td>
                  <td className="hide-sm">{p.opened_at ? <Time ts={p.opened_at}>{ago(p.opened_at)}</Time> : '–'}</td>
                  <td>{p.time_to_merge != null ? human(p.time_to_merge) : '–'}</td>
                  <td>{p.ci_failures}</td>
                </tr>
              ))}</tbody>
          </table>
        ) : <Empty>No PRs in this period.</Empty>}
      </>)}
    </>
  );
}
