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
    <Menu label={`Actions for ${prRef(p)}`} className="icon-btn dots" trigger={<DotsIcon />}>
      {actions}
    </Menu>
  );
}

function DotsIcon() {
  return (
    <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true" fill="currentColor">
      <circle cx="3" cy="8" r="1.5" /><circle cx="8" cy="8" r="1.5" /><circle cx="13" cy="8" r="1.5" />
    </svg>
  );
}
