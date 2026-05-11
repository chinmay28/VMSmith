import { useEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { HardDrive, Download, Trash2, Upload, Pencil } from 'lucide-react';
import { images as imagesApi } from '../api/client';
import { useFetch, useMutation } from '../hooks/useFetch';
import { PageHeader, EmptyState, Spinner, ErrorBanner, Modal, PaginationControls } from '../components/Shared';

const DEFAULT_PER_PAGE = 25;

export default function ImageList() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [showUpload, setShowUpload] = useState(false);
  const [editing, setEditing] = useState(null);
  const [tagFilter, setTagFilter] = useState('');
  const [page, setPage] = useState(1);
  const [perPage, setPerPage] = useState(DEFAULT_PER_PAGE);
  const [sort, setSort] = useState(searchParams.get('sort') || 'id');
  const [order, setOrder] = useState(searchParams.get('order') || 'asc');
  const [selected, setSelected] = useState(() => new Set());
  const [bulkResult, setBulkResult] = useState(null);
  const { data: imageResponse, loading, error, refresh } = useFetch(
    () => imagesApi.list({ page, perPage, tag: tagFilter, sort, order }),
    [page, perPage, tagFilter, sort, order],
    10000,
  );
  const deleteMut = useMutation(imagesApi.delete);
  const bulkMut = useMutation(imagesApi.bulkDelete);
  const imageList = imageResponse?.data || [];
  const totalImages = imageResponse?.meta?.totalCount ?? imageList.length;
  const allTags = useMemo(
    () => [...new Set(imageList.flatMap(img => img.tags || []))].sort(),
    [imageList],
  );

  useEffect(() => { setPage(1); }, [tagFilter, sort, order]);

  useEffect(() => {
    const next = new URLSearchParams(searchParams);
    if (sort && sort !== 'id') next.set('sort', sort); else next.delete('sort');
    if (order && order !== 'asc') next.set('order', order); else next.delete('order');
    setSearchParams(next, { replace: true });
  }, [sort, order]); // eslint-disable-line react-hooks/exhaustive-deps

  // Drop selections that are no longer visible (page/filter/refresh churn).
  useEffect(() => {
    if (!imageList.length) {
      if (selected.size) setSelected(new Set());
      return;
    }
    const existing = new Set(imageList.map(img => img.id));
    let changed = false;
    const next = new Set();
    selected.forEach(id => {
      if (existing.has(id)) next.add(id);
      else changed = true;
    });
    if (changed) setSelected(next);
  }, [imageList]); // eslint-disable-line react-hooks/exhaustive-deps

  const handleDelete = async (id, name) => {
    if (!window.confirm(`Delete image "${name}"?`)) return;
    await deleteMut.execute(id);
    refresh();
  };

  const allSelected = selected.size > 0 && selected.size === imageList.length;
  const someSelected = selected.size > 0 && !allSelected;
  const toggleAll = () => {
    if (allSelected) setSelected(new Set());
    else setSelected(new Set(imageList.map(img => img.id)));
  };
  const toggleOne = (id) => {
    setSelected(prev => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  };
  const handleBulkDelete = async () => {
    if (!selected.size) return;
    const result = await bulkMut.execute({ ids: Array.from(selected) });
    setBulkResult(result);
    setSelected(new Set());
    refresh();
  };
  const dismissBulkResult = () => setBulkResult(null);

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
      <EditImageModal image={editing} onClose={() => setEditing(null)} onSaved={refresh} />

      {allTags.length > 0 && (
        <div className="flex flex-wrap gap-2 mb-4" data-testid="image-tag-filter">
          <button
            className={`btn-ghost text-xs ${tagFilter === '' ? 'text-blue-400' : ''}`}
            onClick={() => setTagFilter('')}
            data-testid="image-tag-filter-all"
          >
            All
          </button>
          {allTags.map(tag => (
            <button
              key={tag}
              className={`badge ${tagFilter === tag ? 'badge-running' : 'bg-steel-800/60 text-steel-300 border-steel-700/40'}`}
              onClick={() => setTagFilter(tag)}
              data-testid={`image-tag-filter-${tag}`}
            >
              #{tag}
            </button>
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2 mb-4 text-xs text-steel-400" data-testid="image-list-sort-controls">
        <span>Sort by</span>
        <select
          value={sort}
          onChange={(e) => setSort(e.target.value)}
          className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-steel-200"
          data-testid="image-list-sort-field"
        >
          <option value="id">ID</option>
          <option value="name">Name</option>
          <option value="size">Size</option>
          <option value="created_at">Created</option>
        </select>
        <select
          value={order}
          onChange={(e) => setOrder(e.target.value)}
          className="bg-steel-900/60 border border-steel-700/60 rounded px-2 py-1 text-steel-200"
          data-testid="image-list-sort-order"
        >
          <option value="asc">Ascending</option>
          <option value="desc">Descending</option>
        </select>
      </div>

      {loading && !imageList ? (
        <div className="flex justify-center py-20"><Spinner size={20} /></div>
      ) : !imageList?.length ? (
        <div className="card">
          <EmptyState
            icon={HardDrive}
            title="No images"
            description={tagFilter ? `No images carry tag "${tagFilter}".` : 'Export a VM to create a portable disk image.'}
          />
        </div>
      ) : (
        <div className="card overflow-hidden">
          <div className="flex items-center justify-between px-4 py-1.5 border-b border-steel-800/40 bg-steel-900/40">
            <label className="flex items-center gap-2 text-xs text-steel-400 cursor-pointer">
              <input
                type="checkbox"
                checked={allSelected}
                ref={(el) => { if (el) el.indeterminate = someSelected; }}
                onChange={toggleAll}
                data-testid="image-select-all"
              />
              {selected.size > 0 ? `${selected.size} selected` : 'Select all'}
            </label>
            <button
              className="btn-ghost text-xs text-red-400 hover:text-red-300 disabled:opacity-40 disabled:cursor-not-allowed"
              onClick={handleBulkDelete}
              disabled={!selected.size || bulkMut.loading}
              data-testid="btn-bulk-delete-images"
            >
              <Trash2 size={12} /> Delete selected
            </button>
          </div>
          {bulkResult && (
            <div
              className="px-4 py-2 border-b border-steel-800/40 bg-steel-900/40 text-xs text-steel-400 flex items-center justify-between"
              data-testid="image-bulk-result"
            >
              <span>
                {(bulkResult.results || []).filter(r => r.success).length} of {(bulkResult.results || []).length} succeeded
                {bulkResult.results?.some(r => !r.success) && (
                  <span className="text-red-400">
                    {' '}· {bulkResult.results.filter(r => !r.success).length} failed
                  </span>
                )}
              </span>
              <button className="btn-ghost text-xs" onClick={dismissBulkResult}>Dismiss</button>
            </div>
          )}
          <table className="w-full" data-testid="image-table">
            <thead>
              <tr className="border-b border-steel-800/40">
                <th className="table-header table-cell w-8"></th>
                <th className="table-header table-cell">Name</th>
                <th className="table-header table-cell">Tags</th>
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
                    <input
                      type="checkbox"
                      checked={selected.has(img.id)}
                      onChange={() => toggleOne(img.id)}
                      data-testid={`image-checkbox-${img.name}`}
                    />
                  </td>
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
                        {img.description && (
                          <p
                            className="text-[11px] text-steel-400 mt-0.5 max-w-[260px] truncate"
                            title={img.description}
                            data-testid={`image-description-${img.name}`}
                          >
                            {img.description}
                          </p>
                        )}
                      </div>
                    </div>
                  </td>
                  <td className="table-cell">
                    {img.tags?.length > 0 ? (
                      <div className="flex flex-wrap gap-1" data-testid={`image-tags-${img.name}`}>
                        {img.tags.map(tag => (
                          <span key={tag} className="badge bg-steel-800/60 text-steel-300 border-steel-700/40">
                            #{tag}
                          </span>
                        ))}
                      </div>
                    ) : (
                      <span className="text-xs text-steel-600">—</span>
                    )}
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
                      <button
                        className="btn-ghost text-xs text-steel-400 hover:text-steel-200"
                        onClick={() => setEditing(img)}
                        data-testid={`btn-edit-image-${img.name}`}
                      >
                        <Pencil size={13} /> Edit
                      </button>
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
  const [description, setDescription] = useState('');
  const [tagsField, setTagsField] = useState('');
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
      const tags = tagsField.split(',').map(t => t.trim()).filter(Boolean);
      await imagesApi.upload(
        file,
        name.trim() || undefined,
        { description: description.trim() || undefined, tags: tags.length ? tags : undefined },
        setUploadProgress,
      );
      onUploaded();
      onClose();
      setFile(null);
      setName('');
      setDescription('');
      setTagsField('');
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

        <div>
          <label className="label">Description</label>
          <input
            className="input"
            placeholder="Optional human-readable note"
            value={description}
            onChange={e => setDescription(e.target.value)}
            data-testid="upload-image-description"
          />
        </div>

        <div>
          <label className="label">Tags</label>
          <input
            className="input"
            placeholder="comma,separated,tags"
            value={tagsField}
            onChange={e => setTagsField(e.target.value)}
            data-testid="upload-image-tags"
          />
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

function EditImageModal({ image, onClose, onSaved }) {
  const [description, setDescription] = useState('');
  const [tagsField, setTagsField] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (image) {
      setDescription(image.description || '');
      setTagsField((image.tags || []).join(','));
      setError('');
    }
  }, [image]);

  if (!image) return null;

  const handleSave = async () => {
    setSaving(true);
    setError('');
    try {
      const patch = {};
      const trimmedDescription = description.trim();
      if (trimmedDescription !== (image.description || '')) {
        patch.description = trimmedDescription;
      }
      const newTags = tagsField.split(',').map(t => t.trim()).filter(Boolean);
      const currentTags = (image.tags || []).join(',');
      if (newTags.join(',') !== currentTags) {
        patch.tags = newTags;
      }
      if (Object.keys(patch).length === 0) {
        onClose();
        return;
      }
      await imagesApi.update(image.id, patch);
      onSaved();
      onClose();
    } catch (e) {
      setError(e.message);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal open={!!image} onClose={onClose} title={`Edit Image — ${image.name}`}>
      <div className="space-y-4" data-testid="edit-image-modal">
        <div>
          <label className="label">Description</label>
          <input
            className="input"
            value={description}
            onChange={e => setDescription(e.target.value)}
            data-testid="edit-image-description"
          />
        </div>
        <div>
          <label className="label">Tags</label>
          <input
            className="input"
            placeholder="comma,separated,tags"
            value={tagsField}
            onChange={e => setTagsField(e.target.value)}
            data-testid="edit-image-tags"
          />
        </div>
        {error && <p className="text-sm text-red-400">Error: {error}</p>}
        <div className="flex justify-end gap-2 pt-2">
          <button className="btn-secondary" onClick={onClose} disabled={saving}>Cancel</button>
          <button className="btn-primary" onClick={handleSave} disabled={saving} data-testid="btn-save-image">
            {saving ? <Spinner size={14} /> : null}
            Save
          </button>
        </div>
      </div>
    </Modal>
  );
}
