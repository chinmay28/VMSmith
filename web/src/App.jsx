import { Routes, Route } from 'react-router-dom';
import AuthGate from './components/AuthGate';
import Layout from './components/Layout';
import Dashboard from './pages/Dashboard';
import VMList from './pages/VMList';
import VMDetail from './pages/VMDetail';
import VMConsole from './pages/VMConsole';
import ImageList from './pages/ImageList';
import TemplateList from './pages/TemplateList';
import Schedules from './pages/Schedules';
import LogViewer from './pages/LogViewer';
import Activity from './pages/Activity';
import Settings from './pages/Settings';

export default function App() {
  return (
    <AuthGate>
      <Routes>
        {/* Full-viewport console — opens in its own tab, no app chrome. */}
        <Route path="/vms/:id/console" element={<VMConsole />} />
        <Route
          path="*"
          element={
            <Layout>
              <Routes>
                <Route path="/" element={<Dashboard />} />
                <Route path="/vms" element={<VMList />} />
                <Route path="/vms/:id" element={<VMDetail />} />
                <Route path="/images" element={<ImageList />} />
                <Route path="/templates" element={<TemplateList />} />
                <Route path="/schedules" element={<Schedules />} />
                <Route path="/activity" element={<Activity />} />
                <Route path="/logs" element={<LogViewer />} />
                <Route path="/settings" element={<Settings />} />
              </Routes>
            </Layout>
          }
        />
      </Routes>
    </AuthGate>
  );
}
