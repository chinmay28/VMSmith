import { Routes, Route } from 'react-router-dom';
import Layout from './components/Layout';
import Dashboard from './pages/Dashboard';
import VMList from './pages/VMList';
import VMDetail from './pages/VMDetail';
import ImageList from './pages/ImageList';

export default function App() {
  return (
    <Layout>
      <Routes>
        <Route path="/" element={<Dashboard />} />
        <Route path="/vms" element={<VMList />} />
        <Route path="/vms/:id" element={<VMDetail />} />
        <Route path="/images" element={<ImageList />} />
      </Routes>
    </Layout>
  );
}
