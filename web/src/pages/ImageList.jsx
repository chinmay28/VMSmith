import { HardDrive, Download, Trash2 } from 'lucide-react';
import { images as imagesApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { PageHeader, EmptyState, Spinner, ErrorBanner } from '../components/Shared';

export default function ImageList() {
  const { data: imageList, loading, error, refresh } = useFetch(() => imagesApi.list(), [], 10000);
  const deleteMut = useMutation(imagesApi.delete);

  const handleDelete = async (id, name) => {
    if (!window.confirm(`Delete image "${name}"?`)) return;
    await deleteMut.execute(id);
    refresh();
  };

  const humanSize = (bytes) => {
    if (bytes >= 1073741824) return `${(bytes / 1073741824).toFixed(1)} GB`;
    if (bytes >= 1048576) return `${(bytes / 1048576).toFixed(1)} MB`;
    return `${bytes} B`;
  };

  return (
    <div>
      <PageHeader
        title="Images"
        subtitle="Portable VM disk images"
      />

      {error && <div className="mb-4"><ErrorBanner message={error} onRetry={refresh} /></div>}

      {loading && !imageList ? (
        <div className="flex justify-center py-20"><Spinner size={20} /></div>
      ) : !imageList?.length ? (
        <div className="card">
          <EmptyState
            icon={HardDrive}
            title="No images"
            description="Export a VM to create a portable disk image."
          />
        </div>
      ) : (
        <div className="card overflow-hidden">
          <table className="w-full">
            <thead>
              <tr className="border-b border-steel-800/40">
                <th className="table-header table-cell">Name</th>
                <th className="table-header table-cell">Format</th>
                <th className="table-header table-cell">Size</th>
                <th className="table-header table-cell">Created</th>
                <th className="table-header table-cell text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {imageList.map(img => (
                <tr key={img.id} className="hover:bg-steel-800/20 transition-colors">
                  <td className="table-cell">
                    <div className="flex items-center gap-2.5">
                      <div className="w-7 h-7 rounded bg-steel-800/60 border border-steel-700/30 flex items-center justify-center">
                        <HardDrive size={13} className="text-steel-500" />
                      </div>
                      <div>
                        <span className="font-mono text-sm text-steel-100">{img.name}</span>
                        {img.source_vm && (
                          <p className="text-[10px] font-mono text-steel-600 mt-0.5">from {img.source_vm}</p>
                        )}
                      </div>
                    </div>
                  </td>
                  <td className="table-cell">
                    <span className="badge bg-steel-800/60 text-steel-400 border-steel-700/40">{img.format}</span>
                  </td>
                  <td className="table-cell font-mono text-xs text-steel-400">
                    {humanSize(img.size_bytes)}
                  </td>
                  <td className="table-cell text-xs text-steel-500">
                    {new Date(img.created_at).toLocaleDateString()}
                  </td>
                  <td className="table-cell text-right">
                    <div className="flex items-center justify-end gap-1">
                      <a
                        href={imagesApi.downloadUrl(img.id)}
                        className="btn-ghost text-xs text-blue-400 hover:text-blue-300"
                        download
                      >
                        <Download size={13} /> Download
                      </a>
                      <button
                        className="btn-ghost text-xs text-red-400 hover:text-red-300"
                        onClick={() => handleDelete(img.id, img.name)}
                      >
                        <Trash2 size={13} />
                      </button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
