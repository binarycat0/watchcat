import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react';
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
  watching: 'ok', stopped: 'idle',
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

// ---- pagination ----

/**
 * Client-side paging over an already loaded list. Goes back to the first
 * page when resetKey changes (e.g. a new filter or period) and clamps the
 * page when the list shrinks under it.
 */
export function usePager<T>(items: T[], pageSize = 20, resetKey?: unknown) {
  const [page, setPage] = useState(0);
  const pages = Math.max(1, Math.ceil(items.length / pageSize));
  useEffect(() => setPage(0), [resetKey]);
  const current = Math.min(page, pages - 1);
  return {
    rows: items.slice(current * pageSize, (current + 1) * pageSize),
    page: current, pages, total: items.length, pageSize, setPage,
  };
}

type PagerProps = {
  page: number;
  setPage: (p: number) => void;
  hasNext: boolean;
  /** Known page count; enables the "last page" button. */
  pages?: number;
  label: string;
};

/** Pager for a list paged on the client (see usePager). */
export function Pager({ pager }: { pager: ReturnType<typeof usePager<unknown>> }) {
  const { page, pages, total, pageSize, setPage } = pager;
  if (pages <= 1) return null;
  const label = `${page * pageSize + 1}–${Math.min(total, (page + 1) * pageSize)} of ${total}`;
  return <PagerBar page={page} setPage={setPage} hasNext={page < pages - 1} pages={pages} label={label} />;
}

/** Pager controls; also used directly for lists paged by the server. */
export function PagerBar({ page, setPage, hasNext, pages, label }: PagerProps) {
  if (page === 0 && !hasNext) return null;
  return (
    <div className="pager">
      <span className="muted small">{label}</span>
      <button type="button" disabled={page === 0} onClick={() => setPage(0)} aria-label="First page">«</button>
      <button type="button" disabled={page === 0} onClick={() => setPage(page - 1)}>Prev</button>
      <span className="small">Page {page + 1}{pages ? ` of ${pages}` : ''}</span>
      <button type="button" disabled={!hasNext} onClick={() => setPage(page + 1)}>Next</button>
      {pages !== undefined && (
        <button type="button" disabled={!hasNext} onClick={() => setPage(pages - 1)} aria-label="Last page">»</button>
      )}
    </div>
  );
}

// ---- dropdown menu ----

/**
 * A button that opens a dropdown. Closes on outside click, Escape, or a click
 * on any item. Clicks don't bubble out, so it's safe inside clickable rows.
 */
export function Menu({ trigger, label, className, children }: {
  trigger: ReactNode;
  label: string;
  className?: string;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const onClick = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onClick);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('mousedown', onClick);
      document.removeEventListener('keydown', onKey);
    };
  }, [open]);
  return (
    <div className="menu" ref={ref} onClick={(e) => e.stopPropagation()}>
      <button type="button" className={className} aria-haspopup="menu" aria-expanded={open}
        aria-label={label} title={label} onClick={() => setOpen((o) => !o)}>
        {trigger}
      </button>
      {open && (
        // Capture so the menu closes even when an item stops propagation;
        // deferred so the item's own click handler still runs first.
        <div className="menu-list" role="menu" onClickCapture={() => window.setTimeout(() => setOpen(false))}>{children}</div>
      )}
    </div>
  );
}
