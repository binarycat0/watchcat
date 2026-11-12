import { createContext, useCallback, useContext, useState, type ReactNode } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';

export type Tone = 'ok' | 'warn' | 'err' | 'info' | 'idle';

const statusTone: Record<string, Tone> = {
  ok: 'ok', merged: 'info', closed: 'idle', unknown: 'idle', paused: 'idle',
  ci_running: 'info', unresolved_comments: 'warn', changes_requested: 'warn',
  ci_failed: 'err', conflict: 'err', error: 'err',
  queued: 'idle', running: 'info', done: 'ok', failed: 'err',
  on: 'ok', 'dry-run': 'info', off: 'idle', warn: 'warn', fail: 'err', skip: 'idle',
  success: 'ok', failure: 'err', cancelled: 'err', timed_out: 'err', superseded: 'idle', skipped: 'idle', neutral: 'idle',
  open: 'info',
};
const statusLabel: Record<string, string> = {
  ok: 'ready', ci_running: 'CI running', ci_failed: 'CI failed', changes_requested: 'changes requested',
  unresolved_comments: 'unresolved comments',
};

export function Pill({ status, label, tone }: { status: string; label?: string; tone?: Tone }) {
  return <span className={`pill ${tone ?? statusTone[status] ?? 'idle'}`}>{label ?? statusLabel[status] ?? status}</span>;
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>;
}

export function Time({ ts, children }: { ts?: string | null; children: ReactNode }) {
  return <span title={ts ? new Date(ts).toLocaleString() : ''}>{children}</span>;
}

// ---- toasts ----

type Toast = { msg: string; err: boolean } | null;
const ToastCtx = createContext<(msg: string, err?: boolean) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<Toast>(null);
  const show = useCallback((msg: string, err = false) => {
    setToast({ msg, err });
    window.clearTimeout((show as unknown as { t?: number }).t);
    (show as unknown as { t?: number }).t = window.setTimeout(() => setToast(null), err ? 6000 : 2500);
  }, []);
  return (
    <ToastCtx.Provider value={show}>
      {children}
      <div id="toast" role="status" aria-live="polite" className={toast ? 'show' + (toast.err ? ' err' : '') : ''}>
        {toast?.msg}
      </div>
    </ToastCtx.Provider>
  );
}

export const useToast = () => useContext(ToastCtx);

/**
 * An action button: runs fn, shows a toast, refreshes all data. Disabled
 * while running so a double click can't queue twice.
 */
export function ActionButton({ fn, ok, children, className, confirm: ask }: {
  fn: () => Promise<unknown>;
  ok?: string | ((r: unknown) => string);
  children: ReactNode;
  className?: string;
  confirm?: string;
}) {
  const toast = useToast();
  const qc = useQueryClient();
  const m = useMutation({
    mutationFn: fn,
    onSuccess: (r) => {
      if (ok) toast(typeof ok === 'function' ? ok(r) : ok);
    },
    onError: (e: Error) => toast(e.message, true),
    onSettled: () => qc.invalidateQueries(),
  });
  return (
    <button
      type="button"
      className={className}
      disabled={m.isPending}
      onClick={(e) => {
        e.stopPropagation();
        if (ask && !window.confirm(ask)) return;
        m.mutate();
      }}
    >
      {children}
    </button>
  );
}
