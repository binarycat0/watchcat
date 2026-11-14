import { useEffect, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { api } from '../api';
import { Empty, SettingsLink, useToast } from '../components';
import type { TemplateInfo } from '../types';

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
          <div>
            <h3>{cur.title} <span className="mono muted">{cur.name}</span></h3>
            <p className="muted small">{cur.description}{cur.name === 'notify_change' && <> Which changes send one is set in <SettingsLink page="config" tab="notifications" />.</>}</p>
          </div>
          <div className="btns">
            <button type="button" disabled={!dirty || busy} onClick={() => edit(cur.text)}>Discard</button>
            <button type="button" disabled={!cur.custom || busy}
              title={cur.custom ? 'Delete your file and use the built-in template' : 'Already the built-in template'}
              onClick={() => window.confirm(`Restore the default ${cur.title} template? Your version is deleted.`) && restore.mutate()}>
              Restore default
            </button>
            <button type="button" className={dirty ? 'primary' : ''} disabled={!dirty || busy || !!previewErr} onClick={() => save.mutate()}>
              Save template
            </button>
          </div>
        </div>
        <p className="muted small">
          {cur.custom ? <>Your version, from <span className="mono">{cur.path}</span>.</> : <>Built-in default. Saving creates <span className="mono">{cur.path}</span>.</>}
          {' '}Applies to the next message; no restart needed.
        </p>
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
