import { useState, useRef } from 'react';
import { HardDrive, Download, Trash2, Upload } from 'lucide-react';
import { images as imagesApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { PageHeader, EmptyState, Spinner, ErrorBanner, Modal, PaginationControls } from '../components/Shared';

const DEFAULT_PER_PAGE = 25;

export default function ImageList() {
  const [showUpload, setShowUpload] = useState(false);
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(DEFAULT_PER_PAGE);
  const { data: imageResponse, loading, error, refresh } = useFetch(
    () => imagesApi.list({ page, perPage }),
    [page, perPage],
    10000,
  );
  const deleteMut = useMutation(imagesApi.delete);
  const imageList = imageResponse?.data || [];
  const totalImages = imageResponse?.meta?.totalCount ?? imageList.length;

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
        subtitle={`${totalImages} portable VM disk image${totalImages === 1 ? '' : 's'}`}
        actions={
          <button className="btn-primary" onClick={() => setShowUpload(true)}>
            <Upload size={15} /> Upload Image
          </button>
        }
      />

      {error && <div className="mb-4"><ErrorBanner message={error} onRetry={refresh} /></div>}

      <UploadImageModal open={showUpload} onClose={() => setShowUpload(false)} onUploaded={refresh} />

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
          <table className="w-full" data-testid="image-table">
            <thead>
              <tr className="border-b border-steel-800/40">
                <th className="table-header table-cell">Name</th>
                <th className="table-header table-cell">Path</th>
                <th className="table-header table-cell">Format</th>
                <th className="table-header table-cell">Size</th>
                <th className="table-header table-cell">Created</th>
                <th className="table-header table-cell text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {imageList.map(img => (
                <tr key={img.id} className="hover:bg-steel-800/20 transition-colors" data-testid={`image-row-${img.name}`}>
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
                  <td className="table-cell font-mono text-xs text-steel-500 max-w-[200px] truncate" title={img.path}>
                    {img.path}
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

      {!!imageList?.length && (
        <PaginationControls
          page={page}
          perPage={perPage}
          total={totalImages}
          itemLabel="images"
          onPageChange={setPage}
          onPerPageChange={(value) => {
            setPerPage(value);
            setPage(1);
          }}
        />
      )}
    </div>
  );
}

function UploadImageModal({ open, onClose, onUploaded }) {
  const [file, setFile] = useState(null);
  const [name, setName] = useState('');
  const [uploading, setUploading] = useState(false);
  const [uploadProgress, setUploadProgress] = useState({ loaded: 0, total: 0, percent: 0 });
  const [error, setError] = useState('');
  const inputRef = useRef(null);

  const handleFile = (f) => {
    setFile(f);
    if (!name && f) {
      const n = f.name.replace(/\.qcow2$/i, '');
      setName(n);
    }
  };

  const handleDrop = (e) => {
    e.preventDefault();
    if (uploading) return;
    const f = e.dataTransfer.files[0];
    if (f) handleFile(f);
  };

  const handleSubmit = async () => {
    if (!file) return;
    setUploading(true);
    setUploadProgress({ loaded: 0, total: file.size || 0, percent: 0 });
    setError('');
    try {
      await imagesApi.upload(file, name.trim() || undefined, setUploadProgress);
      onUploaded();
      onClose();
      setFile(null);
      setName('');
      setUploadProgress({ loaded: 0, total: 0, percent: 0 });
    } catch (e) {
      setError(e.message);
    } finally {
      setUploading(false);
    }
  };

  return (
    <Modal open={open} onClose={onClose} title="Upload Image">
      <div className="space-y-4">
        {/* Drop zone */}
        <div
          className={`border-2 border-dashed rounded-lg p-8 text-center cursor-pointer transition-colors ${
            file ? 'border-forge-500/60 bg-forge-900/10' : 'border-steel-700/50 hover:border-steel-600/60'
          }`}
          onClick={() => !uploading && inputRef.current?.click()}
          onDrop={handleDrop}
          onDragOver={e => e.preventDefault()}
        >
          <input
            ref={inputRef}
            type="file"
            accept=".qcow2,.img,.iso"
            className="hidden"
            onChange={e => handleFile(e.target.files[0])}
            disabled={uploading}
          />
          <Upload size={24} className={`mx-auto mb-2 ${file ? 'text-forge-400' : 'text-steel-600'}`} />
          {file ? (
            <p className="font-mono text-sm text-forge-400">{file.name}</p>
          ) : (
            <>
              <p className="text-sm text-steel-400">Drop a .qcow2 file here, or click to browse</p>
              <p className="text-xs text-steel-600 mt-1">Supported: .qcow2, .img, .iso</p>
            </>
          )}
        </div>

        <div>
          <label className="label">Image Name</label>
          <input
            className="input"
            placeholder="my-image"
            value={name}
            onChange={e => setName(e.target.value)}
          />
          <p className="text-[10px] font-mono text-steel-600 mt-1">Derived from filename if left blank</p>
        </div>

        {uploading && (
          <div className="space-y-2" data-testid="image-upload-progress">
            <div className="flex items-center justify-between text-xs font-mono text-steel-500">
              <span>Uploading…</span>
              <span>{uploadProgress.percent}%</span>
            </div>
            <div className="h-2 rounded-full bg-steel-800/60 overflow-hidden border border-steel-700/40">
              <div
                className="h-full bg-forge-500 transition-all"
                style={{ width: `${uploadProgress.percent}%` }}
                data-testid="image-upload-progress-bar"
              />
            </div>
            {uploadProgress.total > 0 && (
              <p className="text-[10px] font-mono text-steel-600">
                {Math.min(uploadProgress.loaded, uploadProgress.total).toLocaleString()} / {uploadProgress.total.toLocaleString()} bytes
              </p>
            )}
          </div>
        )}

        {error && <p className="text-sm text-red-400">Error: {error}</p>}

        <div className="flex justify-end gap-2 pt-2">
          <button className="btn-secondary" onClick={onClose} disabled={uploading}>Cancel</button>
          <button className="btn-primary" onClick={handleSubmit} disabled={!file || uploading}>
            {uploading ? <Spinner size={14} /> : <Upload size={15} />}
            Upload
          </button>
        </div>
      </div>
    </Modal>
  );
}
