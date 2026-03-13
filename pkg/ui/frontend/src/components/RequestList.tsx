import { FunctionComponent } from 'preact';
import { Request } from '../types';
import { formatLocaleTime } from '../utils/helpers';

interface RequestListProps {
  requests: Request[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onRefresh: () => void;
  loading: boolean;
    appTags: string[];
    selectedAppTag: string | null;
    onAppTagChange: (tag: string) => void;
}

const statusColors = {
  completed: 'bg-green-500',
  failed: 'bg-red-500',
  running: 'bg-blue-500 animate-pulse',
  retrying: 'bg-purple-500 animate-pulse',
};

const RequestList: FunctionComponent<RequestListProps> = ({
  requests,
    selectedId,
    onSelect,
    onRefresh,
    loading,
    appTags,
    selectedAppTag,
    onAppTagChange,
}) => {

    return (
        <div class="col-span-3 bg-gray-900 border-r border-gray-700 flex flex-col min-h-0">
            {/* Header */}
            <div class="bg-gray-800 border-b border-gray-700 h-[52px] flex justify-between items-center px-4">
                <div class="flex items-center gap-2">
                    <svg
                        class="w-5 h-5 text-gray-400"
                        fill="none"
                        stroke="currentColor"
                        viewBox="0 0 24 24"
                    >
                        <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            stroke-width="2"
                            d="M4 6h16M4 10h16M4 14h16M4 18h16"
                        />
                    </svg>
                    <span class="text-gray-100 font-medium">Requests</span>
                </div>
                <div class="flex items-center gap-2">
                    {/* App filter dropdown */}
                    <select
                        value={selectedAppTag || ''}
                        onChange={(e) => onAppTagChange((e.target as HTMLSelectElement).value)}
                        class="bg-gray-700 border border-gray-600 rounded px-2 py-1 text-sm text-white focus:outline-none focus:border-blue-500"
                    >
                        <option value="">All</option>
                        <option value="default">Default (no tag)</option>
                        {appTags.filter(tag => tag !== '').map((tag) => (
                            <option key={tag} value={tag}>
                                {tag}
                            </option>
                        ))}
                    </select>
                </div>
                <button
                    onClick={onRefresh}
                    disabled={loading}
                    class="p-1.5 rounded hover:bg-gray-700 text-gray-400 hover:text-gray-200 transition-colors disabled:opacity-50"
                >
                    <svg
                        class={`w-5 h-5 ${loading ? 'animate-spin' : ''}`}
                        fill="none"
                        stroke="currentColor"
                        viewBox="0 0 24 24"
                    >
                        <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            stroke-width="2"
                            d="M4 4v5h.582m15.356 2A8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
                        />
                    </svg>
                </button>
            </div>

            {/* List */}
            <div class="flex-1 overflow-y-auto min-h-0">
                {requests.length === 0 ? (
                    <div class="text-center text-gray-500 text-sm py-4">
                        No requests
                    </div>
                ) : (
                    requests.map((req) => (
                        <div
                            key={req.id}
                            onClick={() => onSelect(req.id)}
                            class={`p-3 flex items-center gap-3 cursor-pointer hover:bg-gray-800 ${selectedId === req.id ? 'bg-gray-700' : ''}`}
                        >
                            <div class={`w-2 h-2 rounded-full ${statusColors[req.status] || 'bg-gray-500'}`} />
                            <div class="flex-1 min-w-0">
                                <span class="text-sm text-gray-300">{req.model}</span>
                                <div class="text-xs text-gray-400">
                                    {formatLocaleTime(req.startTime)}
                                </div>
                                {req.error && (
                                    <div class="mt-1 text-sm text-red-400 bg-red-900/50 bg-opacity-10 rounded truncate">
                                        ⚠ {req.error}
                                    </div>
                                )}
                            </div>
                        </div>
                    ))
                )}
            </div>
        </div>
    );
};

export default RequestList;