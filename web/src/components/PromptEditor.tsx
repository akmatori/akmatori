import { useState, useEffect, useRef } from 'react';
import { contextApi } from '../api/client';
import type { ContextFile, ValidateReferencesResponse } from '../types';
import { Check, X, FileText, AlertCircle, Loader2 } from 'lucide-react';

interface PromptEditorProps {
  value: string;
  onChange: (value: string) => void;
  placeholder?: string;
  rows?: number;
  className?: string;
}

export default function PromptEditor({
  value = '',
  onChange,
  placeholder = 'Enter skill prompt...',
  rows = 10,
  className = '',
}: PromptEditorProps) {
  const [contextFiles, setContextFiles] = useState<ContextFile[]>([]);
  const [showAutocomplete, setShowAutocomplete] = useState(false);
  const [autocompleteFilter, setAutocompleteFilter] = useState('');
  const [autocompletePosition, setAutocompletePosition] = useState({ top: 0, left: 0 });
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [validation, setValidation] = useState<ValidateReferencesResponse | null>(null);
  const [validating, setValidating] = useState(false);

  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const autocompleteRef = useRef<HTMLDivElement>(null);
  const triggerPositionRef = useRef<number>(0);

  useEffect(() => {
    loadContextFiles();
  }, []);

  const loadContextFiles = async () => {
    try {
      const files = await contextApi.list();
      setContextFiles(files || []);
    } catch (err) {
      console.error('Failed to load context files:', err);
      setContextFiles([]);
    }
  };

  useEffect(() => {
    if (!value) {
      setValidation(null);
      return;
    }

    const timer = setTimeout(() => {
      validateReferences();
    }, 500);
    return () => clearTimeout(timer);
  }, [value]);

  const validateReferences = async () => {
    if (!value || !value.includes('[[')) {
      setValidation(null);
      return;
    }

    try {
      setValidating(true);
      const result = await contextApi.validate(value);
      setValidation(result);
    } catch (err) {
      console.error('Failed to validate references:', err);
    } finally {
      setValidating(false);
    }
  };

  const filteredFiles = (contextFiles || []).filter((file) =>
    file?.filename?.toLowerCase().includes((autocompleteFilter || '').toLowerCase())
  );

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    const newValue = e.target.value;
    const cursorPos = e.target.selectionStart || 0;
    onChange(newValue);
    checkForAutocomplete(newValue, cursorPos);
  };

  const checkForAutocomplete = (text: string, cursorPos: number) => {
    if (!text) {
      setShowAutocomplete(false);
      return;
    }

    const textBeforeCursor = text.slice(0, cursorPos);
    const lastOpenBracket = textBeforeCursor.lastIndexOf('[[');
    const lastCloseBracket = textBeforeCursor.lastIndexOf(']]');

    if (lastOpenBracket > lastCloseBracket) {
      const textAfterBracket = textBeforeCursor.slice(lastOpenBracket + 2);
      if (!textAfterBracket.includes('\n')) {
        triggerPositionRef.current = lastOpenBracket;
        setAutocompleteFilter(textAfterBracket);
        setSelectedIndex(0);
        setShowAutocomplete(true);
        updateAutocompletePosition(cursorPos);
        return;
      }
    }

    setShowAutocomplete(false);
  };

  const updateAutocompletePosition = (cursorPos: number) => {
    if (!textareaRef.current) return;

    try {
      const textarea = textareaRef.current;
      const textValue = textarea.value || '';
      const textBeforeCursor = textValue.slice(0, cursorPos);
      const lines = textBeforeCursor.split('\n');
      const currentLineNumber = lines.length;
      const currentLineText = lines[lines.length - 1] || '';

      const style = window.getComputedStyle(textarea);
      const lineHeight = parseFloat(style.lineHeight) || 20;
      const paddingTop = parseFloat(style.paddingTop) || 12;
      const paddingLeft = parseFloat(style.paddingLeft) || 16;
      const fontSize = parseFloat(style.fontSize) || 14;
      const charWidth = fontSize * 0.6;

      const top = paddingTop + (currentLineNumber * lineHeight) + 4;
      const left = paddingLeft + (currentLineText.length * charWidth);

      const textareaWidth = textarea.offsetWidth || 300;
      const maxLeft = Math.max(0, textareaWidth - 280);

      setAutocompletePosition({
        top: Math.max(0, top - textarea.scrollTop),
        left: Math.min(Math.max(0, left), maxLeft),
      });
    } catch (err) {
      console.error('Error calculating autocomplete position:', err);
      setAutocompletePosition({ top: 30, left: 10 });
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (!showAutocomplete) return;

    const fileCount = filteredFiles.length;

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault();
        setSelectedIndex((prev) => Math.min(prev + 1, Math.max(0, fileCount - 1)));
        break;
      case 'ArrowUp':
        e.preventDefault();
        setSelectedIndex((prev) => Math.max(prev - 1, 0));
        break;
      case 'Enter':
      case 'Tab':
        if (fileCount > 0 && filteredFiles[selectedIndex]) {
          e.preventDefault();
          insertFile(filteredFiles[selectedIndex]);
        }
        break;
      case 'Escape':
        e.preventDefault();
        setShowAutocomplete(false);
        break;
    }
  };

  const insertFile = (file: ContextFile) => {
    if (!file?.filename) return;

    const triggerPos = triggerPositionRef.current;
    const cursorPos = textareaRef.current?.selectionStart || 0;
    const currentValue = value || '';

    const before = currentValue.slice(0, triggerPos);
    const after = currentValue.slice(cursorPos);
    const newValue = `${before}[[${file.filename}]]${after}`;

    onChange(newValue);
    setShowAutocomplete(false);

    setTimeout(() => {
      if (textareaRef.current) {
        const newPos = triggerPos + file.filename.length + 4;
        textareaRef.current.selectionStart = newPos;
        textareaRef.current.selectionEnd = newPos;
        textareaRef.current.focus();
      }
    }, 0);
  };

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (
        autocompleteRef.current &&
        !autocompleteRef.current.contains(e.target as Node) &&
        textareaRef.current &&
        !textareaRef.current.contains(e.target as Node)
      ) {
        setShowAutocomplete(false);
      }
    };

    document.addEventListener('mousedown', handleClickOutside);
    return () => document.removeEventListener('mousedown', handleClickOutside);
  }, []);

  const handleFocus = () => {
    loadContextFiles();
  };

  const safeValue = value || '';

  return (
    <div className="relative">
      <textarea
        ref={textareaRef}
        value={safeValue}
        onChange={handleChange}
        onKeyDown={handleKeyDown}
        onFocus={handleFocus}
        placeholder={placeholder}
        rows={rows}
        className={`input-field font-mono text-sm resize-y ${className}`}
      />

      {/* Autocomplete dropdown */}
      {showAutocomplete && filteredFiles.length > 0 && (
        <div
          ref={autocompleteRef}
          className="absolute z-50 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg shadow-lg max-h-48 overflow-y-auto"
          style={{
            top: autocompletePosition.top,
            left: autocompletePosition.left,
            minWidth: '260px',
          }}
        >
          <div className="px-3 py-2 border-b border-gray-100 dark:border-gray-700 bg-gray-50 dark:bg-gray-900/50">
            <span className="text-xs font-medium text-gray-500 dark:text-gray-400">Context Files</span>
          </div>

          {filteredFiles.map((file, index) => (
            <div
              key={file.id}
              className={`px-3 py-2 cursor-pointer flex items-start gap-3 transition-colors ${
                index === selectedIndex
                  ? 'bg-primary-50 dark:bg-primary-900/20 text-primary-700 dark:text-primary-300'
                  : 'hover:bg-gray-50 dark:hover:bg-gray-700/50 text-gray-700 dark:text-gray-300'
              }`}
              onClick={() => insertFile(file)}
            >
              <FileText size={14} className="flex-shrink-0 mt-0.5 text-primary-500" />
              <div className="flex-1 min-w-0">
                <div className="font-mono text-sm font-medium truncate">{file.filename}</div>
                {file.description && (
                  <div className="text-xs text-gray-500 dark:text-gray-400 truncate mt-0.5">{file.description}</div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* No matches message */}
      {showAutocomplete && filteredFiles.length === 0 && autocompleteFilter && (
        <div
          ref={autocompleteRef}
          className="absolute z-50 bg-white dark:bg-gray-800 border border-gray-200 dark:border-gray-700 rounded-lg shadow-lg px-4 py-3"
          style={{
            top: autocompletePosition.top,
            left: autocompletePosition.left,
            minWidth: '200px',
          }}
        >
          <div className="flex items-center gap-2 text-gray-500 dark:text-gray-400">
            <AlertCircle size={14} />
            <span className="text-sm">No matching files</span>
          </div>
        </div>
      )}

      {/* Reference validation badges */}
      {validation && validation.references && validation.references.length > 0 && (
        <div className="mt-3 flex flex-wrap gap-2">
          {(validation.found || []).map((filename) => (
            <span
              key={filename}
              className="inline-flex items-center gap-1 px-2 py-1 text-xs font-medium rounded-full bg-green-50 dark:bg-green-900/20 text-green-700 dark:text-green-300"
            >
              <Check size={12} />
              <span>{filename}</span>
            </span>
          ))}
          {(validation.missing || []).map((filename) => (
            <span
              key={filename}
              className="inline-flex items-center gap-1 px-2 py-1 text-xs font-medium rounded-full bg-red-50 dark:bg-red-900/20 text-red-700 dark:text-red-300"
            >
              <X size={12} />
              <span>{filename}</span>
              <span className="opacity-60">(missing)</span>
            </span>
          ))}
        </div>
      )}

      {/* Hint */}
      <div className="mt-2 flex items-center gap-3 text-gray-500 dark:text-gray-400">
        <span className="text-xs">
          Type <code className="px-1.5 py-0.5 bg-gray-100 dark:bg-gray-800 rounded text-primary-600 dark:text-primary-400">[[</code> to reference context files
        </span>
        {validating && (
          <span className="flex items-center gap-1 text-xs">
            <Loader2 size={12} className="animate-spin" />
            <span>Validating...</span>
          </span>
        )}
      </div>
    </div>
  );
}
