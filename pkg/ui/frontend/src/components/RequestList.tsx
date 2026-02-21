import { FunctionComponent } from 'preact';
import { Request } from '../types';
import { formatLocaleTime } from '../utils/helpers';

interface RequestListProps {
  requests: Request[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  onRefresh: () => void;
  loading: boolean;
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
              d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15"
            />
          </svg>
        </button>
      </div>

      {/* List */}
      <div class="flex-1 overflow-y-auto min-h-0">
        {requests.length === 0 ? (
          <div class="flex items-center justify-center h-32 text-gray-500">
            No requests yet
          </div>
        ) : (
          <div class="divide-y divide-gray-800">
            {requests.map((request) => (
              <button
                key={request.id}
                onClick={() => onSelect(request.id)}
                class={`w-full text-left px-4 py-3 hover:bg-gray-800 transition-colors relative ${selectedId === request.id
                    ? 'bg-gray-800 border-l-4 border-l-blue-500'
                    : 'border-l-4 border-l-transparent'
                  }`}
              >
                <div class="flex items-center justify-between">
                  <div class="flex items-center gap-2 min-w-0">
                    {/* Status indicator */}
                    <span
                      class={`w-2 h-2 rounded-full flex-shrink-0 ${statusColors[request.status]
                        }`}
                    />
                    {/* Model name */}
                    <span class="text-gray-200 font-medium truncate">
                      {request.model}
                    </span>
                    {/* Retry badge */}
                    {request.retries > 0 && (
                      <span class="px-1.5 py-0.5 text-xs bg-purple-500/20 text-purple-400 rounded">
                        {request.retries} retry{request.retries > 1 ? 's' : ''}
                      </span>
                    )}
                  </div>
                  {/* Timestamp */}
                  <span class="text-gray-500 text-sm flex-shrink-0 ml-2">
                    {formatLocaleTime(request.startTime)}
                  </span>
                </div>
                <div class="flex items-center justify-between mt-1">
                  <span class="text-gray-600 text-xs truncate">
                    {request.id}
                  </span>
                  <span class="text-gray-500 text-xs flex-shrink-0 ml-2">
                    {request.duration}
                  </span>
                </div>
              </button>
            ))}
          </div>
        )}
      </div>
    </div>
  );
};

export default RequestList;
