import { Routes, Route } from 'react-router-dom';
import AuthGate from './components/AuthGate';
import Layout from './components/Layout';
import Dashboard from './pages/Dashboard';
import VMList from './pages/VMList';
import VMDetail from './pages/VMDetail';
import ImageList from './pages/ImageList';
import LogViewer from './pages/LogViewer';
import Activity from './pages/Activity';

export default function App() {
  return (
    <AuthGate>
      <Layout>
        <Routes>
          <Route path="/" element={<Dashboard />} />
          <Route path="/vms" element={<VMList />} />
          <Route path="/vms/:id" element={<VMDetail />} />
          <Route path="/images" element={<ImageList />} />
          <Route path="/activity" element={<Activity />} />
          <Route path="/logs" element={<LogViewer />} />
        </Routes>
      </Layout>
    </AuthGate>
  );
}
