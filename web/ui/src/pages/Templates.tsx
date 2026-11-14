import { useEffect, useState, type ReactNode } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { api } from '../api';
import { Empty, Menu, Pill, SettingsLink, useToast, type ConfigTab, type Tone } from '../components';
import type { Settings, TemplateInfo } from '../types';

const GROUPS: { key: TemplateInfo['group']; label: string }[] = [
  { key: 'notify', label: 'Notifications' },
  { key: 'agent', label: 'Agent prompts' },
];

/** value, but only after it stopped changing for ms. */
function useDebounced<T>(value: T, ms: number): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = window.setTimeout(() => setV(value), ms);
    return () => window.clearTimeout(t);
  }, [value, ms]);
  return v;
}

/**
 * Config > Templates: edit the notifications and agent prompts. Each
 * template saves on its own (to its own file), not with "Save config".
 */
export function TemplatesTab() {
  const list = useQuery({ queryKey: ['templates'], queryFn: api.templates, refetchInterval: false });
  const settings = useQuery({ queryKey: ['settings'], queryFn: api.settings, refetchInterval: false });
  const [params, setParams] = useSearchParams();
  // Unsaved edits, per template, so switching templates doesn't lose them.
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const toast = useToast();
  const qc = useQueryClient();

  const all = list.data ?? [];
  const cur = all.find((t) => t.name === params.get('template')) ?? all[0];
  const text = cur ? drafts[cur.name] ?? cur.text : '';
  const dirty = !!cur && text !== cur.text;
  const preview = useQuery({
    queryKey: ['template-preview', cur?.name, useDebounced(text, 300)],
    queryFn: ({ queryKey }) => api.previewTemplate(queryKey[1] as string, queryKey[2] as string),
    enabled: !!cur,
    refetchInterval: false,
    retry: false,
    placeholderData: (prev) => prev,
  });

  const done = (msg: string) => (t: TemplateInfo) => {
    qc.setQueryData<TemplateInfo[]>(['templates'], (old) => old?.map((x) => (x.name === t.name ? t : x)));
    // Partials change how the prompts that include them render.
    qc.invalidateQueries({ queryKey: ['templates'] });
    setDrafts(({ [t.name]: _, ...rest }) => rest);
    toast(msg);
  };
  const save = useMutation({
    mutationFn: () => api.saveTemplate(cur!.name, text),
    onSuccess: (t) => done(t.custom ? 'Template saved' : 'Same as the default; using the default')(t),
    onError: (e: Error) => toast(e.message, true),
  });
  const restore = useMutation({
    mutationFn: () => api.restoreTemplate(cur!.name),
    onSuccess: done('Default restored'),
    onError: (e: Error) => toast(e.message, true),
  });

  if (list.isError) return <Empty>{(list.error as Error).message}</Empty>;
  if (!cur) return null;
  const pick = (name: string) => setParams({ tab: 'templates', template: name }, { replace: true });
  const edit = (v: string) => setDrafts({ ...drafts, [cur.name]: v });
  const busy = save.isPending || restore.isPending;
  const previewErr = preview.isError ? (preview.error as Error).message : '';

  return (
    <div className="tpl">
      <nav className="tpl-list" aria-label="Templates">
        {GROUPS.map((g) => (
          <div key={g.key}>
            <div className="tpl-group muted small">{g.label}</div>
            {all.filter((t) => t.group === g.key).map((t) => (
              <button key={t.name} type="button" className={'tpl-item' + (t.name === cur.name ? ' on' : '')}
                aria-current={t.name === cur.name} onClick={() => pick(t.name)}>
                <span>{t.title}{t.name in drafts && drafts[t.name] !== t.text && <span className="tab-dot warn" title="Unsaved changes" />}</span>
                {t.error ? <span className="badge fail">broken</span> : t.custom && <span className="badge ok">edited</span>}
              </button>
            ))}
          </div>
        ))}
      </nav>

      <div className="tpl-edit">
        <div className="tpl-head">
          <h3 className="mono" title={cur.title}>{cur.name}</h3>
          <div className="tpl-actions">
            <IconButton label="Discard changes" disabled={!dirty || busy} onClick={() => edit(cur.text)}><IconX /></IconButton>
            <IconButton label={cur.custom ? 'Restore default (deletes your file)' : 'Already the default'} disabled={!cur.custom || busy}
              onClick={() => window.confirm(`Restore the default ${cur.name}? Your version is deleted.`) && restore.mutate()}><IconHistory /></IconButton>
            <IconButton label="Save template" className={dirty ? 'primary' : ''} disabled={!dirty || busy || !!previewErr}
              onClick={() => save.mutate()}><IconSave /></IconButton>
          </div>
        </div>
        <div className="tpl-sub">
          <StatusBadge t={cur} settings={settings.data?.settings} all={all} />
          <span className="muted small">{cur.description}</span>
        </div>
        {cur.error && !dirty && <p className="bad small">The saved file doesn't work, so watchcat uses the default: {cur.error}</p>}

        <textarea className="tpl-text mono" aria-label={`${cur.title} template`} spellCheck={false}
          value={text} onChange={(e) => edit(e.target.value)} rows={Math.min(24, Math.max(8, text.split('\n').length + 1))} />

        <h3>Preview <span className="tab-note">with a sample PR</span></h3>
        {previewErr
          ? <pre className="tpl-preview bad">{previewErr}</pre>
          : <pre className="tpl-preview">{preview.data?.output ?? ''}</pre>}

        <details className="tpl-vars">
          <summary>Variables and functions</summary>
          <table className="health-details">
            <tbody>
              {cur.vars.map((v) => <tr key={v.name}><th className="mono">{v.name}</th><td>{v.desc}</td></tr>)}
              {cur.uses?.map((u) => <tr key={u}><th className="mono">{`{{template "${u}" .}}`}</th><td>includes the shared <button type="button" className="link" onClick={() => pick(u)}>{all.find((t) => t.name === u)?.title ?? u}</button></td></tr>)}
              <tr><th className="mono">link, names, indent, join, lower, upper, trim</th><td>functions, e.g. <span className="mono">{'{{link .PR.URL .PR.Ref}}'}</span> (each notification provider writes links its own way), <span className="mono">{'{{names .FailedChecks}}'}</span>, <span className="mono">{'{{indent "   " .Body}}'}</span></td></tr>
            </tbody>
          </table>
          <p className="muted small">Go <a href="https://pkg.go.dev/text/template" target="_blank" rel="noreferrer">text/template</a> syntax. See the <a href="https://tools.binaryc.at/watchcat/templates/" target="_blank" rel="noreferrer">templates guide</a>.</p>
        </details>
      </div>
    </div>
  );
}

type Status = { label: string; tone: Tone; used: string; tab: ConfigTab };

const modeTone: Record<string, Tone> = { on: 'ok', 'dry-run': 'info', off: 'idle' };

/** Whether a template is in use right now, from the live settings. */
function status(t: TemplateInfo, s: Settings, all: TemplateInfo[]): Status {
  const recipient = !!(s.slack_user_id || s.slack_channel);
  const mode = (rule: string) => s.workflows[rule] || 'off';
  const anyRule = Object.values(s.workflows).some((m) => m === 'on' || m === 'dry-run');
  switch (t.name) {
    case 'notify_change': {
      const on = recipient && s.notify_on.length > 0;
      return {
        label: on ? 'on' : 'off', tone: on ? 'ok' : 'idle', tab: 'notifications',
        used: !recipient ? 'Not sent: no notification recipient is set.'
          : on ? `Sent for ${s.notify_on.length} kind(s) of PR change.` : 'Not sent: no PR changes are picked.',
      };
    }
    case 'notify_rule': {
      const on = recipient && anyRule;
      return {
        label: on ? 'on' : 'off', tone: on ? 'ok' : 'idle', tab: recipient ? 'rules' : 'notifications',
        used: !recipient ? 'Not sent: no notification recipient is set.'
          : on ? 'Sent when a workflow rule in dry-run or on fires.' : 'Not sent: every workflow rule is off.',
      };
    }
    case 'agent_ci_failed':
    case 'agent_review_threads': {
      const rule = t.name.replace('agent_', '');
      const m = mode(rule);
      return {
        label: m, tone: modeTone[m] ?? 'idle', tab: 'rules',
        used: m === 'on' ? `Sent to the agent by the ${rule} rule.`
          : m === 'dry-run' ? `Built by the ${rule} rule and recorded on the task, not sent.` : `Not used: the ${rule} rule is off.`,
      };
    }
    default: {
      const users = all.filter((x) => x.uses?.includes(t.name));
      return {
        label: 'shared', tone: 'info', tab: 'rules',
        used: `Included in ${users.map((x) => x.name).join(' and ')}.`,
      };
    }
  }
}

function StatusBadge({ t, settings, all }: { t: TemplateInfo; settings?: Settings; all: TemplateInfo[] }) {
  if (!settings) return null;
  const st = status(t, settings, all);
  return (
    <Menu className="pill-btn" label={`${t.name}: ${st.label}`} trigger={<Pill status={st.label} tone={st.tone} />}>
      <div className="tpl-pop small">
        <p>{st.used}</p>
        <p>Change it on <SettingsLink page="config" tab={st.tab} />.</p>
        <p className="muted">{t.custom ? 'Your version: ' : 'Built-in default; saving creates '}<span className="mono">{t.path}</span></p>
      </div>
    </Menu>
  );
}

function IconButton({ label, className, disabled, onClick, children }: {
  label: string; className?: string; disabled?: boolean; onClick: () => void; children: ReactNode;
}) {
  return (
    <button type="button" className={'icon-btn tpl-icon ' + (className ?? '')} aria-label={label} title={label}
      disabled={disabled} onClick={onClick}>{children}</button>
  );
}

// Icons in the style of the header's (24px grid, 2px stroke).
const Svg = ({ children }: { children: ReactNode }) => (
  <svg viewBox="0 0 24 24" width="16" height="16" aria-hidden="true" fill="none" stroke="currentColor"
    strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">{children}</svg>
);
const IconX = () => <Svg><path d="M18 6 6 18" /><path d="m6 6 12 12" /></Svg>;
const IconHistory = () => (
  <Svg><path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" /><path d="M3 3v5h5" /><path d="M12 7v5l4 2" /></Svg>
);
const IconSave = () => (
  <Svg>
    <path d="M15.2 3a2 2 0 0 1 1.4.6l3.8 3.8a2 2 0 0 1 .6 1.4V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2z" />
    <path d="M17 21v-7a1 1 0 0 0-1-1H8a1 1 0 0 0-1 1v7" /><path d="M7 3v4a1 1 0 0 0 1 1h7" />
  </Svg>
);
