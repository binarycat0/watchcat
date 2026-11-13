import { api } from './api';
import { ActionButton, Menu } from './components';
import { isFinished, prRef } from './format';
import type { PR } from './types';

/**
 * Watching actions (what watchcat does with the PR: check, pause, stop
 * watching), shared by the PR page (a row of buttons) and the PR list (a
 * dropdown per row) so both offer the same set. Actions on the PR itself,
 * like re-running failed CI, live with their section on the PR page.
 */
export function WatchActions({ p, variant, onRemoved }: {
  p: PR;
  variant: 'buttons' | 'menu';
  onRemoved?: () => void;
}) {
  const item = variant === 'menu' ? 'menu-item' : undefined;
  const actions = (
    <>
      <ActionButton className={item} fn={() => api.check(p.id)} ok="Check queued">Check now</ActionButton>
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
    <Menu label={`Watching actions for ${prRef(p)}`} className="menu-trigger" trigger={<>Watching <span aria-hidden="true">▾</span></>}>
      {actions}
    </Menu>
  );
}
