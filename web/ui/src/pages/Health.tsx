import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { api } from '../api';
import { ActionButton, Empty, Pill, Tabs, healthLabel } from '../components';
import { ago } from '../format';
import type { HealthDetail, HealthResult, HealthStatus } from '../types';

type HealthTab = 'service' | 'vcs' | 'notifications' | 'agent';

// Which checks each tab shows. A check that isn't listed lands on Service.
const TAB_CHECKS: Record<HealthTab, string[]> = {
  service: ['setup'],
  vcs: ['gh'],
  notifications: ['slack', 'slack-cli'],
  agent: ['opencode'],
};
const TABS: { key: HealthTab; label: string }[] = [
  { key: 'service', label: 'Service' },
  { key: 'vcs', label: 'Version control' },
  { key: 'notifications', label: 'Notifications' },
  { key: 'agent', label: 'AI agent' },
];

const isSecret = (d: HealthDetail) => d.label.startsWith('secret ');
const rank: Record<HealthStatus, number> = { fail: 3, warn: 2, ok: 1, skip: 0 };
/** Worst status among the results; skipped (optional, absent) tools don't count as problems. */
const worst = (rs: HealthResult[]): HealthStatus =>
  rs.reduce<HealthStatus>((w, r) => (rank[r.status] > rank[w] ? r.status : w), 'skip');

export function HealthPage() {
  const health = useQuery({ queryKey: ['health'], queryFn: () => api.health(), refetchInterval: 60000 });
  const qc = useQueryClient();
  const [params, setParams] = useSearchParams();
  const tab = (TABS.some((t) => t.key === params.get('tab')) ? params.get('tab') : 'service') as HealthTab;
  const h = health.data;

  // Split the report into tabs. Secrets are listed by the setup check but
  // belong with what uses them: the Slack token goes to Notifications.
  const byTab = (key: HealthTab): HealthResult[] => {
    if (!h) return [];
    const known = Object.values(TAB_CHECKS).flat();
    const rs = h.results.filter((r) => TAB_CHECKS[key].includes(r.name) || (key === 'service' && !known.includes(r.name)));
    const secrets = h.results.find((r) => r.name === 'setup')?.details?.filter(isSecret) ?? [];
    if (key === 'service') return rs.map((r) => (r.name === 'setup' ? { ...r, details: r.details?.filter((d) => !isSecret(d)) } : r));
    if (key === 'notifications' && secrets.length) {
      return [...rs, {
        name: 'secrets', title: 'Secrets', required: false, summary: 'stored in the OS keychain',
        status: secrets.some((d) => d.status === 'fail') ? 'fail' : 'ok', details: secrets.map((d) => ({ ...d, label: d.label.replace('secret ', '') })),
      }];
    }
    return rs;
  };

  return (
    <>
      <div className="header-row">
        <div>
          <div className="pr-title">
            <h1>Health</h1>
            {h && <Pill status={h.status} label={healthLabel[h.status]} />}
          </div>
          {h && <p className="muted small">Same checks as <code>watchcat health</code>, as seen by the daemon, checked {ago(h.checked_at)}.</p>}
        </div>
        <ActionButton ok="Health re-checked" fn={async () => qc.setQueryData(['health'], await api.health(true))}>Check</ActionButton>
      </div>
      {health.isError && <Empty>Could not load the health report: {(health.error as Error).message}</Empty>}
      {h && (
        <div className="tab-block">
          <Tabs<HealthTab>
            active={tab}
            onChange={(t) => setParams(t === 'service' ? {} : { tab: t }, { replace: true })}
            tabs={TABS.map((t) => {
              const st = worst(byTab(t.key));
              return { key: t.key, label: <>{t.label}{(st === 'fail' || st === 'warn') && <span className={`tab-dot ${st}`} aria-label={healthLabel[st]} title={healthLabel[st]} />}</> };
            })}
          />
          <div className="tab-panel" role="tabpanel">
            {byTab(tab).length ? byTab(tab).map((r) => <Check key={r.name} r={r} />) : <Empty>No checks here.</Empty>}
          </div>
        </div>
      )}
    </>
  );
}

function Check({ r }: { r: HealthResult }) {
  return (
    <section className="health-card">
      <div className="health-card-head">
        <Pill status={r.status} label={healthLabel[r.status]} />
        <strong>{r.title}</strong>
        {r.version && <span className="muted small mono">{r.version}</span>}
        <span className="muted small">{r.required ? 'required' : 'optional'}</span>
      </div>
      <div className="muted">{r.summary}</div>
      {!!r.details?.length && (
        <table className="health-details">
          <tbody>{r.details.map((d, i) => (
            <tr key={i}>
              <th scope="row">{d.label}</th>
              <td>{d.value}</td>
            </tr>
          ))}</tbody>
        </table>
      )}
    </section>
  );
}
