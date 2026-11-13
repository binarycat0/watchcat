import { api } from './api';
import { ActionButton, Menu } from './components';
import { isFinished, prRef } from './format';
import type { PR } from './types';

/**
 * The actions on a watched PR, shared by the PR page (a row of buttons) and
 * the PR list (an Actions dropdown per row), so both always offer the same set.
 */
export function PRActions({ p, variant, onRemoved }: {
  p: PR;
  variant: 'buttons' | 'menu';
  onRemoved?: () => void;
}) {
  const failed = (p.snapshot?.failed_checks?.length ?? 0) > 0;
  const item = variant === 'menu' ? 'menu-item' : undefined;
  const actions = (
    <>
      <ActionButton className={item} fn={() => api.check(p.id)} ok="Check queued">Check now</ActionButton>
      {failed && (
        <ActionButton className={item} fn={() => api.retrigger(p.id)} ok="Retrigger queued; see Tasks">Retrigger failed</ActionButton>
      )}
      {!isFinished(p) && (
        <ActionButton className={item} fn={() => api.setPaused(p.id, !p.paused)} ok={p.paused ? 'Resumed' : 'Paused'}>
          {p.paused ? 'Resume' : 'Pause'}
        </ActionButton>
      )}
      <ActionButton className={`danger${item ? ' ' + item : ''}`} confirm={`Stop watching ${prRef(p)}?`}
        fn={async () => { await api.removePR(p.id); onRemoved?.(); }} ok="Removed">Remove</ActionButton>
    </>
  );
  if (variant === 'buttons') return <div className="btns">{actions}</div>;
  return (
    <Menu label={`Actions for ${prRef(p)}`} className="menu-trigger" trigger={<>Actions <span aria-hidden="true">▾</span></>}>
      {actions}
    </Menu>
  );
}
