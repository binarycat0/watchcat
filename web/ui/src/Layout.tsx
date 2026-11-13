import { useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link, NavLink, Outlet, useLocation } from 'react-router-dom';
import { api } from './api';
import { Menu } from './components';

const REPO_URL = 'https://github.com/binarycat0/watchcat';

export function Layout() {
  const status = useQuery({ queryKey: ['status'], queryFn: api.status });
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
        <Link to="/" className="brand" title="Pull requests">
          <span className="logo" aria-hidden="true">🐱</span>
          <span className="brand-text">
            <strong>Watchcat</strong>
            {st && <span className="version">{/^\d/.test(st.version) ? `v${st.version}` : st.version}</span>}
          </span>
        </Link>
        <nav>
          <NavLink to="/tasks">Tasks</NavLink>
          <NavLink to="/stats">Stats</NavLink>
        </nav>
        <div id="meta" className="muted small">
          {status.isError
            ? 'daemon unreachable'
            : st
              ? `up ${st.uptime} · ${st.running.length} running`
              : 'connecting…'}
        </div>
        <div className="top-actions">
          <a className="icon-btn" href={REPO_URL} target="_blank" rel="noopener" title="Watchcat on GitHub" aria-label="Watchcat on GitHub">
            <GitHubIcon />
          </a>
          <SettingsMenu />
        </div>
      </header>
      <main>
        <Outlet />
      </main>
    </>
  );
}

/** Gear dropdown with Health and Config. Shows a dot when tool health isn't ok. */
function SettingsMenu() {
  // Health shells out to gh and Slack, so poll it slowly.
  const health = useQuery({ queryKey: ['health'], queryFn: () => api.health(), refetchInterval: 60000 });
  const { pathname } = useLocation();
  const hs = health.data?.status;
  const active = pathname === '/health' || pathname === '/config';
  return (
    <Menu
      label="Settings"
      className={`icon-btn${active ? ' active' : ''}`}
      trigger={<>
        <GearIcon />
        {hs && hs !== 'ok' && <span className={`menu-dot ${hs}`} aria-hidden="true" />}
      </>}
    >
      <NavLink to="/health" role="menuitem">
        Health <span className={`badge ${hs ?? ''}`}>{hs ?? '?'}</span>
      </NavLink>
      <NavLink to="/config" role="menuitem">Config</NavLink>
    </Menu>
  );
}

function GitHubIcon() {
  return (
    <svg viewBox="0 0 16 16" width="18" height="18" aria-hidden="true" fill="currentColor">
      <path d="M8 0c4.42 0 8 3.58 8 8a8.013 8.013 0 0 1-5.45 7.59c-.4.08-.55-.17-.55-.38 0-.27.01-1.13.01-2.2 0-.75-.25-1.23-.54-1.48 1.78-.2 3.65-.88 3.65-3.95 0-.88-.31-1.59-.82-2.15.08-.2.36-1.02-.08-2.12 0 0-.67-.22-2.2.82-.64-.18-1.32-.27-2-.27-.68 0-1.36.09-2 .27-1.53-1.03-2.2-.82-2.2-.82-.44 1.1-.16 1.92-.08 2.12-.51.56-.82 1.28-.82 2.15 0 3.06 1.86 3.75 3.64 3.95-.23.2-.44.55-.51 1.07-.46.21-1.61.55-2.33-.66-.15-.24-.6-.83-1.23-.82-.67.01-.27.38.01.53.34.19.73.9.82 1.13.16.45.68 1.31 2.69.94 0 .67.01 1.3.01 1.49 0 .21-.15.45-.55.38A7.995 7.995 0 0 1 0 8c0-4.42 3.58-8 8-8Z" />
    </svg>
  );
}

function GearIcon() {
  // Eight teeth around a ring, drawn with strokes so it follows currentColor.
  return (
    <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round">
      {[0, 45, 90, 135, 180, 225, 270, 315].map((a) => (
        <line key={a} x1="12" y1="2.5" x2="12" y2="5" transform={`rotate(${a} 12 12)`} strokeWidth="3" />
      ))}
      <circle cx="12" cy="12" r="6.5" />
      <circle cx="12" cy="12" r="2.5" />
    </svg>
  );
}
