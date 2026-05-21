import { useState } from 'preact/hooks';
import type { MCPServer } from '../../types';
import { escapeHtml } from '../../utils/helpers';
import { useMCPStatus, useMCPServers, createMCPServer, updateMCPServer, deleteMCPServer } from '../../hooks/useApi';
import { MCPServerForm } from './MCPServerForm';

interface MCPServersTabProps {
  setStatus: (status: { type: 'success' | 'error'; message: string } | null) => void;
}

export function MCPServersTab({ setStatus }: MCPServersTabProps) {
  const { status, loading: statusLoading } = useMCPStatus();
  const { servers, loading: serversLoading, refetch } = useMCPServers();
  const [showForm, setShowForm] = useState(false);
  const [serverToEdit, setServerToEdit] = useState<MCPServer | null>(null);
  const [serverToDelete, setServerToDelete] = useState<MCPServer | null>(null);
  const [deleting, setDeleting] = useState(false);

  const handleOpenAdd = () => {
    setServerToEdit(null);
    setShowForm(true);
    setStatus(null);
  };

  const handleOpenEdit = (server: MCPServer) => {
    setServerToEdit(server);
    setShowForm(true);
    setStatus(null);
  };

  const handleSave = async (data: {
    name: string;
    description: string;
    upstream_url: string;
    transport_type: 'sse' | 'streamable_http';
    auth_type: 'none' | 'bearer' | 'basic' | 'api_key';
    auth_token?: string;
    headers: string;
    enabled: boolean;
  }) => {
    try {
      if (serverToEdit) {
        await updateMCPServer(serverToEdit.id, data);
        setStatus({ type: 'success', message: 'MCP server updated successfully' });
      } else {
        await createMCPServer(data);
        setStatus({ type: 'success', message: 'MCP server created successfully' });
      }
      refetch();
      setShowForm(false);
      setServerToEdit(null);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to save MCP server' });
    }
  };

  const handleConfirmDelete = async () => {
    if (!serverToDelete) return;
    try {
      setDeleting(true);
      setStatus(null);
      await deleteMCPServer(serverToDelete.id);
      setStatus({ type: 'success', message: 'MCP server deleted successfully' });
      refetch();
      setServerToDelete(null);
    } catch (e) {
      setStatus({ type: 'error', message: e instanceof Error ? e.message : 'Failed to delete MCP server' });
    } finally {
      setDeleting(false);
    }
  };

  // Loading state for status
  if (statusLoading) {
    return (
      <div class="flex items-center justify-center p-8">
        <div class="animate-spin w-6 h-6 border-2 border-blue-500 border-t-transparent rounded-full"></div>
        <span class="ml-3 text-gray-400">Loading MCP status...</span>
      </div>
    );
  }

  // Check if MCP is enabled
  if (!status?.enabled) {
    return (
      <div class="bg-blue-900/30 border border-blue-800/50 rounded-lg p-6">
        <div class="flex items-start gap-3">
          <svg class="w-5 h-5 text-blue-400 mt-0.5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M13 16h-1v-4h-1m1-4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <div>
            <h3 class="text-blue-300 font-medium mb-1">MCP Proxy is not enabled</h3>
            <p class="text-blue-200/80 text-sm">
              Set MCP_ENABLED=true environment variable to enable MCP server proxying.
            </p>
          </div>
        </div>
      </div>
    );
  }

  // Server list content
  return (
    <div class="space-y-4">
      {!showForm ? (
        <>
          <div class="flex justify-between items-center mb-2">
            <div class="flex items-center gap-2">
              <h3 class="text-white font-medium">MCP Servers</h3>
            </div>
            <button
              onClick={handleOpenAdd}
              class="bg-blue-600 hover:bg-blue-500 text-white text-sm font-medium py-1.5 px-3 rounded-md transition-colors flex items-center gap-1"
            >
              <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 6v6m0 0v6m0-6h6m-6 0H6" />
              </svg>
              Add Server
            </button>
          </div>

          {/* Servers List */}
          <div class="space-y-2">
            {serversLoading ? (
              <div class="flex items-center justify-center p-8">
                <div class="animate-spin w-6 h-6 border-2 border-blue-500 border-t-transparent rounded-full"></div>
                <span class="ml-3 text-gray-400">Loading servers...</span>
              </div>
            ) : servers.length === 0 ? (
              <div class="bg-gray-700/50 rounded-md p-6 border border-gray-700 border-dashed flex flex-col items-center justify-center">
                <svg class="w-10 h-10 text-gray-500 mb-2" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2" />
                </svg>
                <p class="text-gray-400 text-sm">No MCP servers configured</p>
                <p class="text-gray-500 text-xs mt-1">Add your first MCP server to get started</p>
              </div>
            ) : (
              servers.map((server) => (
                <div
                  key={server.id}
                  class="bg-gray-700/80 rounded-md p-4 border border-gray-600/50 hover:bg-gray-700 transition-colors"
                >
                  <div class="flex items-start justify-between gap-4">
                    <div class="flex-1 min-w-0">
                      <div class="flex items-center gap-2 mb-1">
                        <p class="text-gray-100 font-medium truncate">
                          {escapeHtml(server.name)}
                        </p>
                        <span class={`w-2 h-2 rounded-full flex-shrink-0 ${server.enabled ? 'bg-green-500' : 'bg-gray-500'}`}></span>
                      </div>
                      {server.description && (
                        <p class="text-gray-400 text-sm mb-2 truncate">
                          {escapeHtml(server.description)}
                        </p>
                      )}
                      <div class="flex items-center gap-2 flex-wrap text-xs">
                        <span class="text-gray-500 font-mono bg-gray-800/50 px-2 py-0.5 rounded truncate max-w-[300px]" title={server.upstream_url}>
                          {escapeHtml(server.upstream_url)}
                        </span>
                      </div>
                      <div class="flex items-center gap-2 mt-2 flex-wrap">
                        <span class={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium ${
                          server.transport_type === 'sse'
                            ? 'bg-purple-900/50 text-purple-300 border border-purple-800/40'
                            : 'bg-cyan-900/50 text-cyan-300 border border-cyan-800/40'
                        }`}>
                          {server.transport_type === 'sse' ? 'SSE' : 'Streamable HTTP'}
                        </span>
                        <span class="inline-flex items-center px-2 py-0.5 rounded text-xs font-medium bg-gray-800/50 text-gray-300 border border-gray-700/40">
                          {server.auth_type === 'none' ? 'No Auth' : server.auth_type.toUpperCase()}
                        </span>
                      </div>
                    </div>
                    <div class="flex items-center gap-1 flex-shrink-0">
                      <button
                        onClick={() => handleOpenEdit(server)}
                        class="text-gray-400 hover:text-blue-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                        title="Edit server"
                      >
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                        </svg>
                      </button>
                      <button
                        onClick={() => setServerToDelete(server)}
                        class="text-gray-400 hover:text-red-400 transition-colors p-1.5 rounded-md hover:bg-gray-600/50"
                        title="Delete server"
                      >
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                        </svg>
                      </button>
                    </div>
                  </div>
                </div>
              ))
            )}
          </div>
        </>
      ) : (
        <MCPServerForm
          server={serverToEdit}
          onSave={handleSave}
          onCancel={() => {
            setShowForm(false);
            setServerToEdit(null);
          }}
          setStatus={setStatus}
        />
      )}

      {/* Delete Confirmation Dialog */}
      {serverToDelete && (
        <div
          class="fixed inset-0 bg-black/60 backdrop-blur-sm flex items-center justify-center z-[60]"
          onClick={() => setServerToDelete(null)}
        >
          <div
            class="bg-gray-800 rounded-lg shadow-2xl max-w-sm w-full mx-4 border border-gray-700 p-6 flex flex-col items-center text-center"
            onClick={(e) => e.stopPropagation()}
          >
            <div class="w-12 h-12 bg-red-900/30 text-red-400 rounded-full flex items-center justify-center mb-4 border border-red-800/50">
              <svg class="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
              </svg>
            </div>
            <h3 class="text-xl font-semibold text-white mb-2">Delete MCP Server</h3>
            <p class="text-gray-300 mb-6">
              Are you sure you want to delete <span class="font-semibold text-white">"{serverToDelete.name}"</span>? This action cannot be undone.
            </p>
            <div class="flex gap-3 w-full">
              <button
                onClick={() => setServerToDelete(null)}
                class="flex-1 px-4 py-2.5 bg-gray-700 hover:bg-gray-600 text-white rounded-lg transition-colors font-medium border border-gray-600"
                disabled={deleting}
              >
                Cancel
              </button>
              <button
                onClick={handleConfirmDelete}
                class="flex-1 px-4 py-2.5 bg-red-600 hover:bg-red-500 text-white rounded-lg transition-colors font-medium border border-red-500/50 shadow shadow-red-900/20"
                disabled={deleting}
              >
                {deleting ? 'Deleting...' : 'Delete'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
