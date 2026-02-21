import { useState } from 'preact/hooks';
import type { RequestDetail as RequestDetailType } from '../types';
import { escapeHtml } from '../utils/helpers';

import { marked } from 'marked';
import DOMPurify from 'dompurify';

interface RequestDetailProps {
  detail: RequestDetailType | null;
  loading: boolean;
}

function MarkdownContent({ text }: { text: string }) {
  const html = DOMPurify.sanitize(marked.parse(text, { async: false }) as string);
  return (
    <div
      class="prose prose-invert prose-sm max-w-none"
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
}

function CollapsibleText({ text, role }: { text: string; role?: string }) {
  const [isExpanded, setIsExpanded] = useState(false);
  const lines = text ? text.split('\n') : [];

  if (lines.length <= 40) {
    return <MarkdownContent text={text} />;
  }

  if (isExpanded) {
    return (
      <div class="flex flex-col">
        <MarkdownContent text={text} />
        <button
          onClick={() => setIsExpanded(false)}
          class={`mt-3 self-center text-xs px-3 py-1 rounded border transition-colors ${role === 'user'
              ? 'text-gray-300 hover:text-white bg-gray-600/50 border-gray-500/50'
              : 'text-blue-400 hover:text-blue-300 bg-blue-900/20 border-blue-500/30'
            }`}
        >
          Collapse
        </button>
      </div>
    );
  }

  const firstHalf = lines.slice(0, 20).join('\n');
  const secondHalf = lines.slice(-20).join('\n');
  const hiddenCount = lines.length - 40;

  return (
    <div class="flex flex-col">
      <div class="relative overflow-hidden">
        <MarkdownContent text={firstHalf} />
      </div>

      <div class="flex items-center justify-center my-3 opacity-80 hover:opacity-100 transition-opacity">
        <div class={`h-px flex-1 ${role === 'user' ? 'bg-gray-600' : 'bg-blue-800/50'}`}></div>
        <button
          onClick={() => setIsExpanded(true)}
          class={`mx-3 text-xs px-3 py-1 rounded border transition-colors flex-shrink-0 ${role === 'user'
              ? 'text-gray-300 hover:text-white bg-gray-600/50 border-gray-500/50'
              : 'text-blue-400 hover:text-blue-300 bg-blue-900/20 border-blue-500/30'
            }`}
        >
          ... Show {hiddenCount} hidden lines ...
        </button>
        <div class={`h-px flex-1 ${role === 'user' ? 'bg-gray-600' : 'bg-blue-800/50'}`}></div>
      </div>

      <div class="relative overflow-hidden opacity-75">
        <MarkdownContent text={secondHalf} />
      </div>
    </div>
  );
}

export function RequestDetail({ detail, loading }: RequestDetailProps) {
  const [expandedThoughts, setExpandedThoughts] = useState<Set<number>>(new Set());

  const toggleThought = (index: number) => {
    setExpandedThoughts(prev => {
      const next = new Set(prev);
      if (next.has(index)) {
        next.delete(index);
      } else {
        next.add(index);
      }
      return next;
    });
  };

  if (loading) {
    return (
      <div class="flex-[3] bg-gray-800 border-b border-gray-700 flex flex-col">
        <div class="flex-1 flex items-center justify-center text-gray-400">
          Loading...
        </div>
      </div>
    );
  }

  if (!detail) {
    return (
      <div class="flex-[3] bg-gray-800 border-b border-gray-700 flex flex-col">
        <div class="flex-1 flex items-center justify-center text-gray-400">
          Select a request to view details
        </div>
      </div>
    );
  }

  return (
    <div class="flex-[3] bg-gray-800 border-b border-gray-700 flex flex-col min-h-0">
      {/* Header - Fixed, doesn't scroll */}
      <div class="shrink-0 monitor-font text-sm p-4 border-b border-gray-700">
        {/* Header Grid */}
        <div class="grid grid-cols-2 gap-4 mb-4">
          <div>
            <span class="text-gray-400">ID:</span>{' '}
            <span class="text-gray-200">{escapeHtml(detail.id)}</span>
          </div>
          <div>
            <span class="text-gray-400">Status:</span>{' '}
            <span class={
              detail.status === 'completed' ? 'text-green-400' :
                detail.status === 'failed' ? 'text-red-400' :
                  detail.status === 'running' ? 'text-yellow-400' :
                    'text-gray-200'
            }>
              {escapeHtml(detail.status)}
            </span>
          </div>
          <div>
            <span class="text-gray-400">Model:</span>{' '}
            <span class="text-gray-200">{escapeHtml(detail.model)}</span>
          </div>
          <div>
            <span class="text-gray-400">Duration:</span>{' '}
            <span class="text-gray-200">{escapeHtml(detail.duration)}</span>
          </div>
        </div>

        {/* Error Box */}
        {detail.error && (
          <div class="bg-red-900/30 border border-red-500/50 rounded p-3">
            <span class="text-red-400 font-semibold">Error: </span>
            <span class="text-red-300">{escapeHtml(detail.error)}</span>
          </div>
        )}
      </div>

      {/* Messages - Scrollable */}
      <div class="flex-1 overflow-y-auto min-h-0 p-4 monitor-font text-sm">
        <div class="space-y-3">
          {detail.messages.map((message, index) => (
            <div key={index}>
              {/* Message Bubble */}
              <div
                class={`p-3 rounded-lg ${message.role === 'user'
                  ? 'bg-gray-700 ml-0 mr-8'
                  : message.role === 'assistant'
                    ? 'bg-blue-900/40 ml-8 mr-0 border border-blue-500/30'
                    : 'bg-gray-800 mx-4 border border-dashed border-gray-600 italic'
                  }`}
              >
                <div class="text-xs text-gray-500 mb-1 uppercase">
                  {message.role}
                </div>
                <div class="text-gray-200">
                  <CollapsibleText text={message.content} role={message.role} />
                </div>
              </div>

              {/* Thinking - Collapsible */}
              {message.thinking && (
                <details class="ml-8 mr-0 mt-1">
                  <summary
                    class="cursor-pointer text-xs text-purple-400 hover:text-purple-300 flex items-center gap-1"
                    onClick={(e) => {
                      e.preventDefault();
                      toggleThought(index);
                    }}
                  >
                    <span class={`transform transition-transform ${expandedThoughts.has(index) ? 'rotate-90' : ''}`}>
                      ▶
                    </span>
                    Thinking
                  </summary>
                  {expandedThoughts.has(index) && (
                    <div class="mt-2 p-3 bg-purple-900/20 border border-purple-500/30 rounded text-purple-200 text-xs">
                      <CollapsibleText text={message.thinking} role="assistant" />
                    </div>
                  )}
                </details>
              )}

              {/* Tool Calls */}
              {message.tool_calls && message.tool_calls.length > 0 && (
                <div class="ml-8 mr-0 mt-2 space-y-2">
                  {message.tool_calls.map((toolCall, tcIndex) => (
                    <div
                      key={tcIndex}
                      class="bg-purple-900/30 border border-purple-500/40 rounded p-3"
                    >
                      <div class="text-xs text-purple-400 mb-1">Tool Call</div>
                      <div class="text-purple-200">
                        <span class="font-semibold">{escapeHtml(toolCall.function.name)}</span>
                        <pre class="mt-2 text-xs bg-purple-950/50 p-2 rounded overflow-x-auto">
                          {escapeHtml(toolCall.function.arguments)}
                        </pre>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          ))}

          {/* Final Response */}
          {detail.status === 'completed' && detail.response && (
            <div class="mt-4 p-3 bg-green-900/20 border border-green-500/30 rounded-lg ml-8 mr-0">
              <div class="text-xs text-green-500 mb-1 uppercase">Response</div>
              <div class="text-green-200">
                <CollapsibleText text={detail.response} role="assistant" />
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
