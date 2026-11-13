import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from 'react-router-dom';
import { PRActions } from '../PRActions';
import { api } from '../api';
import { Empty, Pill, Time, useToast } from '../components';
import { ago, needsAttention, prRef, prURL, watchState } from '../format';
import type { PR } from '../types';

type Filter = 'all' | 'attention' | 'paused';

export function PRsPage() {
  const prs = useQuery({ queryKey: ['prs'], queryFn: api.prs });
  const [filter, setFilter] = useState<Filter>('all');
  const list = prs.data ?? [];
  const counts = { all: list.length, attention: list.filter(needsAttention).length, paused: list.filter((p) => watchState(p) === 'paused').length };
  const shown = list.filter((p) => filter === 'all' || (filter === 'attention' ? needsAttention(p) : watchState(p) === 'paused'));

  return (
    <>
      <h1>Pull requests</h1>
      <div className="toolbar stacked">
        <AddForm />
        <div className="chips">
          {(['all', 'attention', 'paused'] as Filter[]).map((f) => (
            <button key={f} className={`chip ${filter === f ? 'on' : ''}`} onClick={() => setFilter(f)}>
              {{ all: 'All', attention: 'Needs attention', paused: 'Paused' }[f]} {counts[f]}
            </button>
          ))}
        </div>
      </div>
      {prs.isError && <Empty>{(prs.error as Error).message}</Empty>}
      {prs.isSuccess && (shown.length ? (
        <table>
          <thead><tr><th>PR</th><th>Status</th><th>Watching</th><th /></tr></thead>
          <tbody>{shown.map((p) => <Row key={p.id} p={p} />)}</tbody>
        </table>
      ) : (
        <Empty>{list.length ? 'Nothing matches this filter.' : 'No PRs watched yet. Paste one above.'}</Empty>
      ))}
    </>
  );
}

function Row({ p }: { p: PR }) {
  const navigate = useNavigate();
  return (
    <tr className="click" onClick={(e) => { if (!(e.target as HTMLElement).closest('a')) navigate(`/pr/${p.id}`); }}>
      <td>
        <a href={prURL(p)} target="_blank" rel="noopener">{prRef(p)}</a>
        <span className="title">{p.snapshot?.title}</span>
      </td>
      <td>
        <Pill status={p.status} />
        {/* Closed and merged PRs have the state itself as summary; don't repeat it. */}
        {p.summary && p.summary !== p.status && <div className="muted small">{p.summary}</div>}
      </td>
      <td>
        <Pill status={watchState(p)} />
        <div className="muted small">
          checked <Time ts={p.last_checked}>{ago(p.last_checked)}</Time>
          {watchState(p) === 'watching' && <>, next <Time ts={p.next_check}>{ago(p.next_check)}</Time></>}
        </div>
      </td>
      <td className="actions"><PRActions p={p} variant="menu" /></td>
    </tr>
  );
}

function AddForm() {
  const [ref, setRef] = useState('');
  const toast = useToast();
  const qc = useQueryClient();
  const add = useMutation({
    mutationFn: api.addPR,
    onSuccess: (pr) => { setRef(''); toast(`Watching ${prRef(pr)}`); },
    onError: (e: Error) => toast(e.message, true),
    onSettled: () => qc.invalidateQueries(),
  });
  return (
    <form onSubmit={(e) => { e.preventDefault(); if (ref.trim()) add.mutate(ref.trim()); }}>
      <input value={ref} onChange={(e) => setRef(e.target.value)} autoComplete="off" aria-label="PR to watch"
        placeholder="PR URL (github.com or Enterprise) or owner/repo#123" />
      <button className="primary" disabled={add.isPending}>Watch</button>
    </form>
  );
}
