import { useCallback, useEffect, useState } from 'react';
import { CheckCircle2, Loader2, Save, XCircle, Zap } from 'lucide-react';
import { integrationsApi } from '../../api/client';
import { getErrorMessage } from '../../utils/errors';
import type { Integration, MessagingProvider } from '../../types';
import LoadingSpinner from '../LoadingSpinner';
import ErrorMessage from '../ErrorMessage';
import { areCredentialsValidForCreate, extractCredentialsForCreate, getProviderConfig } from '../settings/integrationsHelpers';

interface EditProps {
  integrationUUID: string;
  onChange: () => void;
}

interface TestState {
  ok: boolean;
  account?: string;
  error?: string;
}

// IntegrationPanel edits the messaging integration of a connector: its
// name, credentials (secrets are never shown; empty keeps them) and the
// enabled flag, and tests the bot credentials.
export default function IntegrationPanel({ integrationUUID, onChange }: EditProps) {
  const [row, setRow] = useState<Integration | null>(null);
  const [values, setValues] = useState<Record<string, string>>({});
  const [name, setName] = useState('');
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<TestState | null>(null);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);

  const load = useCallback(async () => {
    try {
      const r = await integrationsApi.get(integrationUUID);
      setRow(r);
      setName(r.name);
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to load the integration'));
    }
  }, [integrationUUID]);

  useEffect(() => {
    void load();
  }, [load]);

  if (!row) return error ? <ErrorMessage message={error} /> : <LoadingSpinner />;
  const config = getProviderConfig(row.provider);
  const creds = extractCredentialsForCreate(config ?? { provider: row.provider, label: '', iconText: '', available: true, description: '', credentialFields: [] }, values);
  const dirty = name.trim() !== row.name || Object.keys(creds).length > 0;

  const save = async () => {
    setSaving(true);
    setError('');
    setSaved(false);
    try {
      const r = await integrationsApi.update(row.uuid, {
        name: name.trim(),
        ...(Object.keys(creds).length > 0 ? { credentials: creds } : {}),
      });
      setRow(r);
      setValues({});
      setSaved(true);
      onChange();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to save'));
    } finally {
      setSaving(false);
    }
  };

  const toggle = async () => {
    try {
      const r = await integrationsApi.update(row.uuid, { enabled: !row.enabled });
      setRow(r);
      onChange();
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to update'));
    }
  };

  const runTest = async () => {
    setTesting(true);
    setTest(null);
    try {
      setTest(await integrationsApi.test(row.uuid));
    } catch (err) {
      setTest({ ok: false, error: getErrorMessage(err, 'Test failed') });
    } finally {
      setTesting(false);
    }
  };

  return (
    <section className="card space-y-4">
      <div className="flex items-center justify-between gap-3 flex-wrap">
        <h2 className="text-sm font-semibold text-fg">{config?.label ?? row.provider} connection</h2>
        <label className="flex items-center gap-2 text-sm text-fg-muted cursor-pointer">
          <input type="checkbox" checked={row.enabled} onChange={toggle} />
          Enabled
        </label>
      </div>
      {config?.description && <p className="text-xs text-fg-subtle -mt-2">{config.description}</p>}
      {error && <ErrorMessage message={error} />}
      <div>
        <label htmlFor="integ-name" className="block text-sm font-medium text-fg mb-1">Name</label>
        <input id="integ-name" className="input-field" value={name} onChange={(e) => setName(e.target.value)} />
      </div>
      {config?.credentialFields.map((f) => (
        <div key={f.name}>
          <label htmlFor={`integ-${f.name}`} className="block text-sm font-medium text-fg mb-1">{f.label}</label>
          <input
            id={`integ-${f.name}`}
            type={f.secret ? 'password' : 'text'}
            autoComplete="off"
            className="input-field"
            value={values[f.name] ?? ''}
            placeholder={row.credentials?.[f.name] ? '•••••••• (stored; leave empty to keep)' : f.placeholder}
            onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
          />
          {f.hint && <p className="mt-1 text-xs text-fg-subtle">{f.hint}</p>}
        </div>
      ))}
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn btn-primary" onClick={save} disabled={!dirty || saving || !name.trim()}>
          {saving ? <Loader2 className="w-4 h-4 animate-spin" /> : <Save className="w-4 h-4" />}Save
        </button>
        <button className="btn btn-secondary" onClick={runTest} disabled={testing}>
          {testing ? <Loader2 className="w-4 h-4 animate-spin" /> : <Zap className="w-4 h-4" />}Test connection
        </button>
        {saved && <span className="text-sm text-state-ok">Saved.</span>}
        {test && (
          <span className={`flex items-center gap-1.5 text-sm ${test.ok ? 'text-fg' : 'text-state-fail'}`}>
            {test.ok ? <CheckCircle2 className="w-4 h-4 text-state-ok" /> : <XCircle className="w-4 h-4" />}
            {test.ok ? `Connected${test.account ? ` as ${test.account}` : ''}` : test.error || 'Failed'}
          </span>
        )}
      </div>
    </section>
  );
}

interface CreateProps {
  provider: MessagingProvider;
  onCreated: (integration: Integration) => void;
}

// NewIntegrationForm creates the messaging integration of a new connector.
export function NewIntegrationForm({ provider, onCreated }: CreateProps) {
  const config = getProviderConfig(provider);
  const [name, setName] = useState(config?.label ?? provider);
  const [values, setValues] = useState<Record<string, string>>({});
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  if (!config) return <ErrorMessage message={`Unknown messaging provider ${provider}`} />;
  const valid = name.trim() !== '' && areCredentialsValidForCreate(config, values);

  const create = async () => {
    setSaving(true);
    setError('');
    try {
      const row = await integrationsApi.create({
        provider,
        name: name.trim(),
        credentials: extractCredentialsForCreate(config, values),
        enabled: true,
      });
      onCreated(row);
    } catch (err) {
      setError(getErrorMessage(err, 'Failed to add the connector'));
      setSaving(false);
    }
  };

  return (
    <section className="card space-y-4 max-w-2xl">
      <p className="text-sm text-fg-muted">{config.description}</p>
      {error && <ErrorMessage message={error} />}
      <div>
        <label htmlFor="new-integ-name" className="block text-sm font-medium text-fg mb-1">Name</label>
        <input id="new-integ-name" className="input-field" value={name} onChange={(e) => setName(e.target.value)} />
      </div>
      {config.credentialFields.map((f) => (
        <div key={f.name}>
          <label htmlFor={`new-integ-${f.name}`} className="block text-sm font-medium text-fg mb-1">{f.label}</label>
          <input
            id={`new-integ-${f.name}`}
            type={f.secret ? 'password' : 'text'}
            autoComplete="off"
            className="input-field"
            placeholder={f.placeholder}
            value={values[f.name] ?? ''}
            onChange={(e) => setValues({ ...values, [f.name]: e.target.value })}
          />
          {f.hint && <p className="mt-1 text-xs text-fg-subtle">{f.hint}</p>}
        </div>
      ))}
      <button className="btn btn-primary" onClick={create} disabled={!valid || saving}>
        {saving && <Loader2 className="w-4 h-4 animate-spin" />}Add {config.label}
      </button>
    </section>
  );
}
