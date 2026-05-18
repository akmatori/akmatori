import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { AuthProvider } from './context/AuthContext';
import { ThemeProvider } from './context/ThemeContext';
import ProtectedRoute from './components/ProtectedRoute';
import Layout from './components/Layout';
import Login from './pages/Login';
import Setup from './pages/Setup';
import Dashboard from './pages/Dashboard';
import Incidents from './pages/Incidents';
import IncidentDetail from './pages/IncidentDetail';
import Skills from './pages/Skills';
import Tools from './pages/Tools';
import ContextFiles from './pages/ContextFiles';
import Runbooks from './pages/Runbooks';
import Settings from './pages/Settings';
import IntegrationsPage from './pages/IntegrationsPage';
import ChannelsPage from './pages/ChannelsPage';

function App() {
  return (
    <ThemeProvider>
      <AuthProvider>
        <BrowserRouter>
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
                      <Route path="/incidents/:uuid" element={<IncidentDetail />} />
                      <Route path="/incidents" element={<Incidents />} />
                      <Route path="/skills" element={<Skills />} />
                      <Route path="/tools" element={<Tools />} />
                      <Route path="/context" element={<ContextFiles />} />
                      <Route path="/runbooks" element={<Runbooks />} />
                      <Route path="/settings/integrations" element={<IntegrationsPage />} />
                      <Route path="/settings/channels" element={<ChannelsPage />} />
                      <Route path="/settings/slack" element={<Navigate to="/settings/integrations" replace />} />
                      <Route path="/settings" element={<Settings />} />
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
