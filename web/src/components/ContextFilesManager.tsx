import { useState, useEffect, useRef } from 'react';
import { contextApi } from '../api/client';
import type { ContextFile } from '../types';
import { Upload, FileText, Download, Trash2, AlertCircle, Check, X } from 'lucide-react';
import ErrorMessage from './ErrorMessage';
import { SuccessMessage } from './ErrorMessage';

const ALLOWED_EXTENSIONS = ['.md', '.txt', '.json', '.yaml', '.yml', '.xml', '.csv', '.log', '.conf', '.cfg', '.ini', '.sh', '.py', '.pdf'];
const MAX_FILE_SIZE = 10 * 1024 * 1024;
const FILENAME_PATTERN = /^[a-zA-Z0-9][a-zA-Z0-9_-]*\.[a-zA-Z0-9]+$/;

interface ContextFilesManagerProps {
  onFilesChange?: () => void;
}

export default function ContextFilesManager({ onFilesChange }: ContextFilesManagerProps) {
  const [files, setFiles] = useState<ContextFile[]>([]);
  const [loading, setLoading] = useState(true);
  const [uploading, setUploading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);

  const [selectedFile, setSelectedFile] = useState<File | null>(null);
  const [sanitizedFilename, setSanitizedFilename] = useState('');
  const [description, setDescription] = useState('');
  const [fileError, setFileError] = useState<string | null>(null);
  const [dragActive, setDragActive] = useState(false);

  const fileInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    loadFiles();
  }, []);

  const loadFiles = async () => {
    try {
      setLoading(true);
      const data = await contextApi.list();
      setFiles(data);
      setError(null);
    } catch (err) {
      setError('Failed to load context files');
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const validateFilename = (name: string): string | null => {
    if (!name) return 'Filename is required';
    if (name.length > 255) return 'Filename too long (max 255 characters)';
    if (!FILENAME_PATTERN.test(name)) return 'Invalid filename. Use only letters, numbers, dashes, underscores, and a valid extension';
    const ext = '.' + name.split('.').pop()?.toLowerCase();
    if (!ALLOWED_EXTENSIONS.includes(ext)) return `File type not allowed. Allowed: ${ALLOWED_EXTENSIONS.join(', ')}`;
    if (files.some(f => f.filename === name)) return 'A file with this name already exists';
    return null;
  };

  const handleFileSelect = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (file) processFile(file);
  };

  const processFile = (file: File) => {
    // Sanitize the original filename
    const sanitized = file.name.replace(/[^a-zA-Z0-9._-]/g, '-').replace(/--+/g, '-').replace(/^-|-$/g, '');
    const validationError = validateFilename(sanitized);

    setSelectedFile(file);
    setSanitizedFilename(sanitized);
    setFileError(validationError);
  };

  const handleDrag = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    if (e.type === 'dragenter' || e.type === 'dragover') setDragActive(true);
    else if (e.type === 'dragleave') setDragActive(false);
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setDragActive(false);
    if (e.dataTransfer.files && e.dataTransfer.files[0]) processFile(e.dataTransfer.files[0]);
  };

  const handleUpload = async () => {
    if (!selectedFile || !sanitizedFilename || fileError) return;

    if (selectedFile.size > MAX_FILE_SIZE) {
      setError(`File too large. Maximum size is ${MAX_FILE_SIZE / 1024 / 1024} MB`);
      return;
    }

    try {
      setUploading(true);
      setError(null);
      await contextApi.upload(selectedFile, sanitizedFilename, description || undefined);
      setSuccess(`File "${sanitizedFilename}" uploaded successfully`);
      setTimeout(() => setSuccess(null), 3000);

      setSelectedFile(null);
      setSanitizedFilename('');
      setDescription('');
      setFileError(null);
      if (fileInputRef.current) fileInputRef.current.value = '';

      await loadFiles();
      onFilesChange?.();
    } catch (err: any) {
      setError(err.message || 'Failed to upload file');
    } finally {
      setUploading(false);
    }
  };

  const handleDelete = async (file: ContextFile) => {
    if (!confirm(`Delete "${file.filename}"? This cannot be undone.`)) return;

    try {
      await contextApi.delete(file.id);
      setSuccess(`File "${file.filename}" deleted`);
      setTimeout(() => setSuccess(null), 3000);
      await loadFiles();
      onFilesChange?.();
    } catch (err: any) {
      setError(err.message || 'Failed to delete file');
    }
  };

  const formatFileSize = (bytes: number): string => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
  };

  const getFileExtension = (filename: string): string => {
    const ext = filename.split('.').pop()?.toUpperCase() || 'TXT';
    return ext.length > 4 ? ext.slice(0, 4) : ext;
  };

  return (
    <div>
      {error && <ErrorMessage message={error} />}
      {success && <SuccessMessage message={success} />}

      {/* Upload Form */}
      <div className="card mb-8">
        <h3 className="text-lg font-semibold text-gray-900 dark:text-white mb-4">Upload New File</h3>

        <div className="space-y-4">
          {/* Drag & Drop Zone */}
          <div
            className={`relative border-2 border-dashed rounded-lg p-8 text-center transition-all cursor-pointer ${
              dragActive
                ? 'border-primary-500 bg-primary-50 dark:bg-primary-900/20'
                : selectedFile
                ? 'border-green-500 bg-green-50 dark:bg-green-900/20'
                : 'border-gray-300 dark:border-gray-600 hover:border-gray-400 dark:hover:border-gray-500'
            }`}
            onDragEnter={handleDrag}
            onDragLeave={handleDrag}
            onDragOver={handleDrag}
            onDrop={handleDrop}
            onClick={() => fileInputRef.current?.click()}
          >
            <input
              ref={fileInputRef}
              type="file"
              onChange={handleFileSelect}
              accept={ALLOWED_EXTENSIONS.join(',')}
              className="hidden"
            />
            {selectedFile ? (
              <div className="flex items-center justify-center gap-3">
                <Check size={24} className={fileError ? 'text-yellow-500' : 'text-green-500'} />
                <div className="text-left">
                  <p className="font-medium text-gray-900 dark:text-white">{sanitizedFilename}</p>
                  <p className="text-sm text-gray-500 dark:text-gray-400">
                    {formatFileSize(selectedFile.size)}
                    {selectedFile.name !== sanitizedFilename && (
                      <span className="ml-2 text-xs">(from {selectedFile.name})</span>
                    )}
                  </p>
                  {fileError && (
                    <p className="text-sm text-red-600 dark:text-red-400 flex items-center gap-1 mt-1">
                      <AlertCircle size={14} />
                      {fileError}
                    </p>
                  )}
                </div>
                <button
                  onClick={(e) => {
                    e.stopPropagation();
                    setSelectedFile(null);
                    setSanitizedFilename('');
                    setFileError(null);
                    if (fileInputRef.current) fileInputRef.current.value = '';
                  }}
                  className="ml-4 p-1 text-gray-400 hover:text-red-500 transition-colors"
                >
                  <X size={18} />
                </button>
              </div>
            ) : (
              <>
                <Upload size={32} className="mx-auto text-gray-400 mb-3" />
                <p className="font-medium text-gray-700 dark:text-gray-300 mb-1">
                  Drop file here or click to select
                </p>
                <p className="text-sm text-gray-500 dark:text-gray-400">
                  Allowed: {ALLOWED_EXTENSIONS.join(', ')} | Max: 10 MB
                </p>
              </>
            )}
          </div>

          {/* Description Input */}
          <div>
            <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
              Description
              <span className="ml-2 font-normal text-gray-500">(optional, shown in autocomplete)</span>
            </label>
            <input
              type="text"
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="e.g., Kubernetes troubleshooting guide"
              className="input-field"
            />
          </div>

          {/* Upload Button */}
          <div className="flex justify-end pt-2">
            <button
              onClick={handleUpload}
              disabled={uploading || !selectedFile || !sanitizedFilename || !!fileError}
              className="btn btn-primary"
            >
              <Upload size={16} />
              {uploading ? 'Uploading...' : 'Upload'}
            </button>
          </div>
        </div>
      </div>

      {/* Files List */}
      <div>
        <h3 className="text-lg font-semibold text-gray-900 dark:text-white mb-4">
          Uploaded Files ({files.length})
        </h3>

        {loading ? (
          <div className="py-12 text-center">
            <div className="inline-block w-8 h-8 rounded-full border-3 border-gray-200 dark:border-gray-700 border-t-primary-500 animate-spin" />
            <p className="mt-4 text-gray-500 dark:text-gray-400 text-sm">Loading...</p>
          </div>
        ) : files.length === 0 ? (
          <div className="py-16 text-center border-2 border-dashed border-gray-300 dark:border-gray-600 rounded-lg">
            <FileText size={48} className="mx-auto text-gray-400 mb-4" />
            <p className="text-gray-500 dark:text-gray-400">No context files uploaded yet</p>
          </div>
        ) : (
          <div className="card divide-y divide-gray-100 dark:divide-gray-700 p-0 overflow-hidden">
            {files.map((file) => (
              <div
                key={file.id}
                className="p-4 flex items-center justify-between hover:bg-gray-50 dark:hover:bg-gray-800/50 transition-colors"
              >
                <div className="flex items-center gap-4">
                  <div className="w-12 h-12 rounded-lg bg-primary-50 dark:bg-primary-900/20 flex items-center justify-center text-xs font-bold text-primary-600 dark:text-primary-400">
                    {getFileExtension(file.filename)}
                  </div>
                  <div>
                    <div className="font-medium text-gray-900 dark:text-white">{file.filename}</div>
                    {file.description && (
                      <div className="text-sm text-gray-500 dark:text-gray-400 mt-0.5">{file.description}</div>
                    )}
                    <div className="text-xs text-gray-400 dark:text-gray-500 mt-1">
                      {formatFileSize(file.size)}
                      {file.original_name !== file.filename && (
                        <span className="ml-2">(from {file.original_name})</span>
                      )}
                    </div>
                  </div>
                </div>

                <div className="flex items-center gap-2">
                  <a
                    href={contextApi.getDownloadUrl(file.id)}
                    className="btn btn-ghost text-primary-600 dark:text-primary-400 hover:bg-primary-50 dark:hover:bg-primary-900/20"
                    title="Download"
                  >
                    <Download size={18} />
                  </a>
                  <button
                    onClick={() => handleDelete(file)}
                    className="btn btn-ghost text-red-600 dark:text-red-400 hover:bg-red-50 dark:hover:bg-red-900/20"
                    title="Delete"
                  >
                    <Trash2 size={18} />
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* Usage hint */}
      <div className="mt-6 p-4 rounded-lg bg-primary-50 dark:bg-primary-900/20 border border-primary-200 dark:border-primary-800">
        <p className="text-sm text-primary-800 dark:text-primary-200">
          <span className="font-semibold">Usage:</span> Reference files in skill prompts using{' '}
          <code className="px-1.5 py-0.5 bg-white dark:bg-gray-800 rounded text-primary-600 dark:text-primary-400">[[filename]]</code>{' '}
          syntax. Example:{' '}
          <code className="px-1.5 py-0.5 bg-white dark:bg-gray-800 rounded text-primary-600 dark:text-primary-400">Read [[runbook.md]] for guidance.</code>
        </p>
      </div>
    </div>
  );
}
