import { useState } from 'preact/hooks';
import type { MCPServer } from '../../types';
import { testMCPServer } from '../../hooks/useApi';

interface MCPServerFormProps {
  server?: MCPServer | null;
  onSave: (data: {
    id?: string; // Only included for create operations
    name: string;
    description: string;
    upstream_url: string;
    transport_type: 'sse' | 'streamable_http';
    auth_type: 'none' | 'bearer' | 'basic' | 'api_key';
    auth_token?: string;
    headers: string;
    enabled: boolean;
  }) => Promise<void>;
  onCancel: () => void;
  setStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
}

// ID validation: lowercase alphanumeric, hyphens, underscores, must start and end with letter or number
const ID_PATTERN = /^[a-z0-9]([a-z0-9_-]*[a-z0-9])?$/;
const ID_PATTERN_MSG = 'Lowercase letters, numbers, hyphens, and underscores. Must start and end with a letter or number.';

function validateServerId(id: string): string | null {
  if (!id.trim()) {
    return 'Server ID is required';
  }
  if (id.length > 128) {
    return 'Server ID must be at most 128 characters';
  }
  if (!ID_PATTERN.test(id)) {
    return ID_PATTERN_MSG;
  }
  return null;
}

export function MCPServerForm({ server, onSave, onCancel, setStatus }: MCPServerFormProps) {
  const isEdit = !!server;
  const [serverId, setServerId] = useState(server?.id || '');
  const [serverIdTouched, setServerIdTouched] = useState(false);
  const [serverIdError, setServerIdError] = useState<string | null>(null);
  const [name, setName] = useState(server?.name || '');
  const [description, setDescription] = useState(server?.description || '');
  const [upstreamUrl, setUpstreamUrl] = useState(server?.upstream_url || '');
  const [transportType, setTransportType] = useState<'sse' | 'streamable_http'>(server?.transport_type || 'sse');
  const [authType, setAuthType] = useState<'none' | 'bearer' | 'basic' | 'api_key'>(server?.auth_type || 'none');
  const [authToken, setAuthToken] = useState('');
  const [tokenModified, setTokenModified] = useState(false);
  const [headers, setHeaders] = useState(server?.headers || '');
  const [enabled, setEnabled] = useState(server?.enabled ?? true);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ success: boolean; latency?: number; error?: string } | null>(null);

  // Validate server ID when it changes
  const handleServerIdChange = (value: string) => {
    setServerId(value);
    setServerIdTouched(true);
    setServerIdError(validateServerId(value));
  };

  const handleSubmit = async (e: Event) => {
    e.preventDefault();

    // Validate server ID for create mode
    if (!isEdit) {
      const idError = validateServerId(serverId);
      setServerIdError(idError);
      setServerIdTouched(true);
      if (idError) {
        setStatus({ type: 'error', message: idError });
        return;
      }
    }

    try {
      setSaving(true);
      setStatus(null);
      const payload: {
        id?: string;
        name: string;
        description: string;
        upstream_url: string;
        transport_type: 'sse' | 'streamable_http';
        auth_type: 'none' | 'bearer' | 'basic' | 'api_key';
        auth_token?: string;
        headers: string;
        enabled: boolean;
      } = {
        // Only include id for create operations (immutable after creation)
        ...(isEdit ? {} : { id: serverId }),
        name,
        description,
        upstream_url: upstreamUrl,
        transport_type: transportType,
        auth_type: authType,
        headers,
        enabled,
      };
      // Only include auth_token if it's create mode or user actually modified it
      if (!isEdit || tokenModified) {
        payload.auth_token = authType !== 'none' ? authToken : undefined;
      }
      await onSave(payload);
    } finally {
      setSaving(false);
    }
  };

  const handleTest = async () => {
    if (!upstreamUrl) {
      setStatus({ type: 'error', message: 'Please enter an upstream URL' });
      return;
    }
    setTestResult(null);
    try {
      setTesting(true);
      setStatus(null);
      if (!server) {
        // Can't test without saving first - no server ID
        setTestResult({ success: false, error: 'Save the server first, then test the connection' });
        return;
      }
      const result = await testMCPServer(server.id);
      setTestResult({ success: result.success, latency: result.latency_ms });
    } catch (err) {
      setTestResult({ success: false, error: String(err) });
    } finally {
      setTesting(false);
    }
  };

  return (
    <div class="bg-gray-800 rounded-lg border border-gray-700 p-6">
      <h3 class="text-lg font-semibold text-white mb-4">
        {isEdit ? 'Edit MCP Server' : 'Add MCP Server'}
      </h3>
      <form onSubmit={handleSubmit} class="space-y-4">
        {isEdit ? (
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">
              Server ID
            </label>
            <div class="px-3 py-2 bg-gray-900 border border-gray-600 rounded-md text-gray-400 font-mono text-sm">
              {server?.id}
            </div>
            <p class="text-xs text-gray-500 mt-1">Server ID cannot be changed after creation</p>
          </div>
        ) : (
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">
              Server ID <span class="text-red-400">*</span>
            </label>
            <input
              type="text"
              value={serverId}
              onInput={(e) => handleServerIdChange((e.target as HTMLInputElement).value)}
              required
              maxLength={128}
              class={`w-full px-3 py-2 bg-gray-700 border rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent font-mono ${
                serverIdError && serverIdTouched ? 'border-red-500' : 'border-gray-600'
              }`}
              placeholder="my-first-mcp-server"
            />
            {serverIdError && serverIdTouched ? (
              <p class="text-xs text-red-400 mt-1">{serverIdError}</p>
            ) : (
              <p class="text-xs text-gray-500 mt-1">{ID_PATTERN_MSG}</p>
            )}
          </div>
        )}

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Name <span class="text-red-400">*</span>
          </label>
          <input
            type="text"
            value={name}
            onInput={(e) => setName((e.target as HTMLInputElement).value)}
            required
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            placeholder="e.g., My MCP Server"
          />
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Description
          </label>
          <input
            type="text"
            value={description}
            onInput={(e) => setDescription((e.target as HTMLInputElement).value)}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            placeholder="Optional description"
          />
        </div>

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Upstream URL <span class="text-red-400">*</span>
          </label>
          <input
            type="text"
            value={upstreamUrl}
            onInput={(e) => setUpstreamUrl((e.target as HTMLInputElement).value)}
            required
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            placeholder="https://mcp.example.com/sse"
          />
        </div>

        <div class="grid grid-cols-2 gap-4">
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">
              Transport Type
            </label>
            <select
              value={transportType}
              onChange={(e) => setTransportType((e.target as HTMLSelectElement).value as 'sse' | 'streamable_http')}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            >
              <option value="sse">SSE</option>
              <option value="streamable_http">Streamable HTTP</option>
            </select>
          </div>

          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">
              Auth Type
            </label>
            <select
              value={authType}
              onChange={(e) => setAuthType((e.target as HTMLSelectElement).value as 'none' | 'bearer' | 'basic' | 'api_key')}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
            >
              <option value="none">None</option>
              <option value="bearer">Bearer Token</option>
              <option value="basic">Basic Auth</option>
              <option value="api_key">API Key</option>
            </select>
          </div>
        </div>

        {authType !== 'none' && (
          <div>
            <label class="block text-sm font-medium text-gray-300 mb-1">
              Auth Token
            </label>
            <input
              type="password"
              value={tokenModified ? authToken : ''}
              onInput={(e) => {
                setAuthToken((e.target as HTMLInputElement).value);
                setTokenModified(true);
              }}
              class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent"
              placeholder={isEdit ? 'Token set (hidden)' : authType === 'bearer' ? 'Bearer token' : authType === 'basic' ? 'username:password' : 'API key'}
            />
          </div>
        )}

        <div>
          <label class="block text-sm font-medium text-gray-300 mb-1">
            Custom Headers <span class="text-gray-500 font-normal">(JSON)</span>
          </label>
          <textarea
            value={headers}
            onInput={(e) => setHeaders((e.target as HTMLTextAreaElement).value)}
            rows={3}
            class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-md text-white placeholder-gray-400 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent font-mono text-sm"
            placeholder='{"Authorization": "Bearer xxx"}'
          />
          <p class="text-xs text-gray-500 mt-1">JSON format for custom HTTP headers</p>
        </div>

        <div class="flex items-center gap-2">
          <button
            type="button"
            onClick={handleTest}
            disabled={testing || !upstreamUrl}
            class="px-4 py-2 bg-gray-700 hover:bg-gray-600 text-white text-sm font-medium rounded-md transition-colors border border-gray-600 disabled:opacity-50 disabled:cursor-not-allowed flex items-center gap-1"
          >
            <svg class={`w-4 h-4 ${testing ? 'animate-spin' : ''}`} fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 10V3L4 14h7v7l9-11h-7z" />
            </svg>
            {testing ? 'Testing...' : 'Test Connection'}
          </button>
          {testResult && (
            testResult.success ? (
              <span class="text-sm text-green-400">Connected ({testResult.latency}ms)</span>
            ) : (
              <span class="text-sm text-red-400">{testResult.error}</span>
            )
          )}
        </div>

        <div class="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setEnabled(!enabled)}
            class={`w-10 h-6 rounded-full flex-shrink-0 relative transition-colors ${enabled ? 'bg-green-500' : 'bg-gray-500'
              }`}
          >
            <span class={`absolute top-1 w-4 h-4 bg-white rounded-full transition-all ${enabled ? 'right-1' : 'left-1'
              }`}></span>
          </button>
          <span class="text-sm text-gray-300">
            {enabled ? 'Enabled' : 'Disabled'}
          </span>
        </div>

        <div class="flex gap-3 pt-2">
          <button
            type="button"
            onClick={onCancel}
            class="flex-1 px-4 py-2.5 bg-gray-700 hover:bg-gray-600 text-white rounded-lg transition-colors font-medium border border-gray-600"
            disabled={saving}
          >
            Cancel
          </button>
          <button
            type="submit"
            class="flex-1 px-4 py-2.5 bg-blue-600 hover:bg-blue-500 text-white rounded-lg transition-colors font-medium border-blue-500/50 shadow shadow-blue-900/20"
            disabled={saving || !name || !upstreamUrl || (!isEdit && (serverIdError !== null || !serverId.trim()))}
          >
            {saving ? 'Saving...' : isEdit ? 'Save Changes' : 'Add Server'}
          </button>
        </div>
      </form>
    </div>
  );
}
