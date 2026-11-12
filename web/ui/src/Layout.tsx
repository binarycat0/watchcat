import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, NavLink, Outlet, useLocation } from 'react-router-dom';
import { api } from './api';

export function Layout() {
  const status = useQuery({ queryKey: ['status'], queryFn: api.status });
  // Health shells out to gh and Slack, so poll it slowly.
  const health = useQuery({ queryKey: ['health'], queryFn: () => api.health(), refetchInterval: 60000 });
  const { pathname } = useLocation();
  // Block body on purpose: an effect must return nothing or a cleanup
  // function, and some browsers return a value from scrollTo.
  useEffect(() => {
    window.scrollTo(0, 0);
  }, [pathname]);

  const st = status.data;
  return (
    <>
      <header className="top">
        <div className="brand">
          <span className="logo" aria-hidden="true" />
          <strong>Watchcat</strong>
          <Link to="/health" className={`badge ${health.data?.status ?? ''}`} title="Tool health">
            tools {health.data?.status ?? '?'}
          </Link>
        </div>
        <nav>
          <NavLink to="/" end>Pull requests</NavLink>
          <NavLink to="/tasks">Tasks</NavLink>
          <NavLink to="/report">Report</NavLink>
          <NavLink to="/health">Health</NavLink>
          <NavLink to="/settings">Settings</NavLink>
        </nav>
        <div id="meta" className="muted small">
          {status.isError
            ? 'daemon unreachable'
            : st
              ? `v${st.version} · up ${st.uptime} · ${st.prs} PRs · ${st.running.length} running · Slack ${st.slack ? 'on' : 'off'}`
              : 'connecting…'}
        </div>
      </header>
      <main>
        <Outlet />
      </main>
    </>
  );
}
