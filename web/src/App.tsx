import { BrowserRouter, Routes, Route, Navigate, useLocation } from 'react-router-dom';
import { AuthProvider } from './context/AuthContext';
import { ThemeProvider } from './context/ThemeContext';
import ProtectedRoute from './components/ProtectedRoute';
import Layout from './components/Layout';
import ApiStatusBanner from './components/ApiStatusBanner';
import Login from './pages/Login';
import Setup from './pages/Setup';
import Dashboard from './pages/Dashboard';
import Incidents from './pages/Incidents';
import Skills from './pages/Skills';
import Connectors from './pages/Connectors';
import ConnectorDetail from './pages/ConnectorDetail';
import ConnectorNew from './pages/ConnectorNew';
import ContextFiles from './pages/ContextFiles';
import Runbooks from './pages/Runbooks';
import Settings from './pages/Settings';
import CronJobsPage from './pages/CronJobsPage';
import Events from './pages/Events';
import RequestDetail from './pages/RequestDetail';
import Proposals from './pages/Proposals';
import ProposalDetail from './pages/ProposalDetail';

// FeedRedirect keeps old /feed links (and their ?search= / ?event= query)
// working after the page moved to /events.
function FeedRedirect() {
  const { search } = useLocation();
  return <Navigate to={{ pathname: '/events', search }} replace />;
}

function App() {
  return (
    <ThemeProvider>
      <AuthProvider>
        <BrowserRouter>
          <ApiStatusBanner />
          <Routes>
            <Route path="/login" element={<Login />} />
            <Route path="/setup" element={<Setup />} />
            <Route
              path="/*"
              element={
                <ProtectedRoute>
                  <Layout>
                    <Routes>
                      <Route path="/" element={<Dashboard />} />
                      <Route path="/incidents/:uuid?" element={<Incidents />} />
                      <Route path="/events" element={<Events />} />
                      <Route path="/requests/:uuid" element={<RequestDetail />} />
                      <Route path="/feed" element={<FeedRedirect />} />
                      <Route path="/proposals/:uuid" element={<ProposalDetail />} />
                      <Route path="/proposals" element={<Proposals />} />
                      <Route path="/skills" element={<Skills />} />
                      <Route path="/connectors" element={<Connectors />} />
                      <Route path="/connectors/new/:catalogId" element={<ConnectorNew />} />
                      <Route path="/connectors/:uuid" element={<ConnectorDetail />} />
                      <Route path="/tools" element={<Navigate to="/connectors" replace />} />
                      <Route path="/context" element={<ContextFiles />} />
                      <Route path="/runbooks" element={<Runbooks />} />
                      <Route path="/cron" element={<CronJobsPage />} />
                      <Route path="/settings/slack" element={<Navigate to="/connectors" replace />} />
                      <Route path="/settings/integrations" element={<Navigate to="/connectors" replace />} />
                      <Route path="/settings/channels" element={<Navigate to="/connectors" replace />} />
                      <Route path="/settings/alert-sources" element={<Navigate to="/connectors" replace />} />
                      <Route path="/settings/:section?" element={<Settings />} />
                    </Routes>
                  </Layout>
                </ProtectedRoute>
              }
            />
          </Routes>
        </BrowserRouter>
      </AuthProvider>
    </ThemeProvider>
  );
}

export default App;
