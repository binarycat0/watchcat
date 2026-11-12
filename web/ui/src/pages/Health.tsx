import { useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from '../api';
import { ActionButton, Empty, Pill } from '../components';
import { ago } from '../format';

/** One full-width table: a header row per tool, its details beneath. */
export function HealthPage() {
  const health = useQuery({ queryKey: ['health'], queryFn: () => api.health(), refetchInterval: 60000 });
  const qc = useQueryClient();
  const h = health.data;
  return (
    <>
      <div className="header-row">
        <div>
          <h1>Tool health</h1>
          {h && <p className="muted">As seen by the daemon, checked {ago(h.checked_at)}. Overall: <Pill status={h.status} /></p>}
        </div>
        <div className="btns">
          <ActionButton ok="Health re-checked"
            fn={async () => qc.setQueryData(['health'], await api.health(true))}>Re-check</ActionButton>
        </div>
      </div>
      {health.isError && <Empty>Could not load the health report: {(health.error as Error).message}</Empty>}
      {h && (
        <table className="health">
          <colgroup><col style={{ width: 72 }} /><col style={{ width: '30%' }} /><col /></colgroup>
          {h.results.map((r) => (
            <tbody key={r.name} className={`health-group ${r.status}`}>
              <tr className="health-head">
                <td className="st"><Pill status={r.status} /></td>
                <td><strong>{r.title}</strong> {r.version && <span className="muted small mono">{r.version}</span>}{' '}
                  <span className="muted small">· {r.required ? 'required' : 'optional'}</span></td>
                <td>{r.summary}</td>
              </tr>
              {(r.details ?? []).map((d, i) => (
                <tr key={i} className="health-detail">
                  <td className="st">{d.status === 'fail' && <span className="dot bad" title="problem" />}</td>
                  <td className="muted">{d.label}</td>
                  <td className={d.status === 'fail' ? 'bad' : ''}>{d.value}</td>
                </tr>
              ))}
            </tbody>
          ))}
        </table>
      )}
    </>
  );
}
