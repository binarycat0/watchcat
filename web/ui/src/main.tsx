import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { HashRouter, Navigate, Route, Routes } from 'react-router-dom';
import { ToastProvider } from './components';
import { Layout } from './Layout';
import { PRsPage } from './pages/PRs';
import { PRPage } from './pages/PR';
import { TasksPage } from './pages/Tasks';
import { ReportPage } from './pages/Report';
import { HealthPage } from './pages/Health';
import { SettingsPage } from './pages/Settings';
import './styles.css';

// React Query polls every 5s while the tab is visible and keeps showing the
// last data while refetching, so typing in a form is never interrupted.
const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchInterval: 5000, refetchIntervalInBackground: false, retry: 1 } },
});

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <HashRouter>
          <Routes>
            <Route element={<Layout />}>
              <Route index element={<PRsPage />} />
              <Route path="pr/:id" element={<PRPage />} />
              <Route path="tasks" element={<TasksPage />} />
              <Route path="report" element={<ReportPage />} />
              <Route path="health" element={<HealthPage />} />
              <Route path="settings" element={<SettingsPage />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Route>
          </Routes>
        </HashRouter>
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
);
