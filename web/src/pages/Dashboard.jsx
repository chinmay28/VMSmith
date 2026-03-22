import { useNavigate } from 'react-router-dom';
import { Server, HardDrive, Activity, Plus } from 'lucide-react';
import { vms, images as imagesApi } from '../api/client';
import { useFetch } from '../hooks/useFetch';
import { PageHeader, StatCard, StatusBadge, Spinner, ErrorBanner, EmptyState } from '../components/Shared';

export default function Dashboard() {
  const { data: vmList, loading: vmLoading, error: vmError } = useFetch(() => vms.list(), [], 5000);
  const { data: imageList, loading: imgLoading } = useFetch(() => imagesApi.list(), [], 10000);
  const navigate = useNavigate();

  const runningCount = (vmList || []).filter(v => v.state === 'running').length;
  const totalCount = (vmList || []).length;
  const imageCount = (imageList || []).length;

  return (
    <div>
      <PageHeader
        title="Dashboard"
        subtitle="System overview"
        actions={
          <button className="btn-primary" onClick={() => navigate('/vms?create=1')}>
            <Plus size={15} /> New Machine
          </button>
        }
      />

      {/* Stats */}
      <div className="grid grid-cols-3 gap-3 mb-6">
        <div data-testid="stat-total"><StatCard label="Total Machines" value={vmLoading ? '—' : totalCount} icon={Server} /></div>
        <div data-testid="stat-running"><StatCard label="Running" value={vmLoading ? '—' : runningCount} icon={Activity} accent /></div>
        <div data-testid="stat-images"><StatCard label="Images" value={imgLoading ? '—' : imageCount} icon={HardDrive} /></div>
      </div>

      {/* Recent machines */}
      <div className="card">
        <div className="px-4 py-3 border-b border-steel-800/40">
          <h2 className="text-sm font-display font-semibold text-steel-300">Machines</h2>
        </div>

        {vmError && <div className="p-4"><ErrorBanner message={vmError} /></div>}

        {vmLoading ? (
          <div className="flex justify-center py-12"><Spinner size={20} /></div>
        ) : !vmList?.length ? (
          <EmptyState
            icon={Server}
            title="No machines yet"
            description="Create your first virtual machine to get started."
            action={
              <button className="btn-primary" onClick={() => navigate('/vms?create=1')}>
                <Plus size={15} /> Create Machine
              </button>
            }
          />
        ) : (
          <table data-testid="dashboard-vm-table" className="w-full">
            <thead>
              <tr className="border-b border-steel-800/40">
                <th className="table-header table-cell">Name</th>
                <th className="table-header table-cell">Status</th>
                <th className="table-header table-cell">IP</th>
                <th className="table-header table-cell">CPU / RAM</th>
              </tr>
            </thead>
            <tbody>
              {vmList.map(vm => (
                <tr
                  key={vm.id}
                  data-testid={`vm-row-${vm.name}`}
                  className="cursor-pointer hover:bg-steel-800/30 transition-colors"
                  onClick={() => navigate(`/vms/${vm.id}`)}
                >
                  <td className="table-cell">
                    <span className="font-mono text-steel-100 text-sm">{vm.name}</span>
                  </td>
                  <td className="table-cell"><StatusBadge state={vm.state} /></td>
                  <td className="table-cell font-mono text-xs text-steel-400">
                    {vm.ip || '—'}
                  </td>
                  <td className="table-cell font-mono text-xs text-steel-400">
                    {vm.spec.cpus} vCPU · {vm.spec.ram_mb} MB
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}
