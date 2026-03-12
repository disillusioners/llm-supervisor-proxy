import { useState, useRef, useEffect } from 'preact/hooks';
import type { RequestDetail as RequestDetailType } from '../types';
import { escapeHtml, escapeHtmlLight, generateCurlCommand } from '../utils/helpers';

import { marked } from 'marked';
import DOMPurify from 'dompurify';

interface RequestDetailProps {
  detail: RequestDetailType | null;
  loading: boolean;
}

// Tool Call Display Component - Collapsible with formatted arguments
function ToolCallDisplay({ toolCall }: { toolCall: { function: { name: string; arguments: string } } }) {
  const [isExpanded, setIsExpanded] = useState(false);
  const [parsedArgs, setParsedArgs] = useState<unknown>(null);
  const [parseError, setParseError] = useState(false);

  // Parse arguments on mount
  useEffect(() => {
    try {
      const parsed = JSON.parse(toolCall.function.arguments);
      setParsedArgs(parsed);
      setParseError(false);
    } catch {
      setParsedArgs(null);
      setParseError(true);
    }
  }, [toolCall.function.arguments]);

  const toggleExpand = () => setIsExpanded(!isExpanded);

  return (
    <div class="bg-purple-900/20 border border-purple-500/30 rounded-lg overflow-hidden transition-all duration-200 hover:border-purple-500/50 hover:bg-purple-900/30">
      {/* Header - Always visible, clickable */}
      <button
        type="button"
        onClick={toggleExpand}
        class="w-full flex items-center justify-between p-3 text-left hover:bg-purple-800/20 transition-colors"
      >
        <div class="flex items-center gap-2">
          <span class="text-lg" role="img" aria-label="tool">🔧</span>
          <span class="text-purple-200 font-semibold text-sm">
            {escapeHtml(toolCall.function.name)}
          </span>
        </div>
        <div class="flex items-center gap-2">
          {parsedArgs && !parseError && (
            <span class="text-xs text-purple-400/70">
              {Array.isArray(parsedArgs) 
                ? `${parsedArgs.length} args` 
                : typeof parsedArgs === 'object' 
                  ? `${Object.keys(parsedArgs).length} keys`
                  : ''}
            </span>
          )}
          <span class={`text-purple-400 text-xs transition-transform duration-200 ${isExpanded ? 'rotate-90' : ''}`}>
            ▶
          </span>
        </div>
      </button>

      {/* Arguments - Collapsible */}
      <div 
        class={`overflow-hidden transition-all duration-300 ease-in-out ${
          isExpanded ? 'max-h-[600px] opacity-100' : 'max-h-0 opacity-0'
        }`}
      >
        <div class="px-3 pb-3">
          <div class="text-xs text-purple-400/60 mb-2 ml-6">Arguments</div>
          <div class="bg-purple-950/50 rounded border border-purple-500/20 p-3 ml-6">
            {parseError ? (
              <pre class="text-xs text-red-400 overflow-x-auto">
                {escapeHtml(toolCall.function.arguments)}
              </pre>
            ) : parsedArgs ? (
              <JsonViewer data={parsedArgs} depth={0} defaultExpanded={true} />
            ) : (
              <span class="text-gray-500 text-xs">Loading...</span>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

// Collapsible JSON viewer component
function JsonViewer({ 
  data, 
  depth = 0, 
  defaultExpanded = true 
}: { 
  data: unknown; 
  depth?: number; 
  defaultExpanded?: boolean;
}) {
  const [expanded, setExpanded] = useState(defaultExpanded || depth < 2);

  if (data === null) {
    return <span class="text-gray-500">null</span>;
  }

  if (typeof data === 'boolean') {
    return <span class="text-purple-400">{data.toString()}</span>;
  }

  if (typeof data === 'number') {
    return <span class="text-orange-400">{data}</span>;
  }

  if (typeof data === 'string') {
    return <span class="text-green-400">"{escapeHtml(data)}"</span>;
  }

  if (Array.isArray(data)) {
    if (data.length === 0) {
      return <span class="text-gray-400">[]</span>;
    }

    return (
      <span>
        <button 
          onClick={() => setExpanded(!expanded)}
          class="text-gray-400 hover:text-gray-200 mr-1 text-xs"
        >
          {expanded ? '▼' : '▶'}[{data.length}]
        </button>
        {expanded && (
          <div class="ml-4 border-l border-gray-600 pl-2">
            {data.map((item, i) => (
              <div key={i}>
                <span class="text-gray-500 text-xs">{i}: </span>
                <JsonViewer data={item} depth={depth + 1} defaultExpanded={false} />
              </div>
            ))}
          </div>
        )}
      </span>
    );
  }

  if (typeof data === 'object') {
    const entries = Object.entries(data as Record<string, unknown>);
    if (entries.length === 0) {
      return <span class="text-gray-400">{}</span>;
    }

    return (
      <span>
        <button 
          onClick={() => setExpanded(!expanded)}
          class="text-gray-400 hover:text-gray-200 mr-1 text-xs"
        >
          {expanded ? '▼' : '▶'}{'{'}
        </button>
        {!expanded && <span class="text-gray-400">{entries.length} keys{'}'}</span>}
        {expanded && (
          <div class="ml-4 border-l border-gray-600 pl-2">
            {entries.map(([key, value]) => (
              <div key={key}>
                <span class="text-cyan-400">{escapeHtml(key)}: </span>
                <JsonViewer data={value} depth={depth + 1} defaultExpanded={false} />
              </div>
            ))}
            <span class="text-gray-400">{'}'}</span>
          </div>
        )}
      </span>
    );
  }

  return <span class="text-gray-400">{String(data)}</span>;
}

// Modal for displaying cURL command
function CurlModal({ 
  detail, 
  onClose 
}: { 
  detail: RequestDetailType; 
  onClose: () => void;
}) {
  const modalRef = useRef<HTMLDivElement>(null);
  const [copied, setCopied] = useState(false);
  const [proxyUrl, setProxyUrl] = useState('http://localhost:8080/v1/chat/completions');

  // Close on escape key
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onClose]);

  // Close on backdrop click
  const handleBackdropClick = (e: MouseEvent) => {
    if (e.target === modalRef.current) onClose();
  };

  const curlCommand = generateCurlCommand(
    detail.model,
    detail.messages,
    detail.parameters,
    detail.is_stream,
    proxyUrl
  );

  const handleCopy = async () => {
    try {
      await navigator.clipboard.writeText(curlCommand);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Fallback for older browsers
      const textArea = document.createElement('textarea');
      textArea.value = curlCommand;
      document.body.appendChild(textArea);
      textArea.select();
      document.execCommand('copy');
      document.body.removeChild(textArea);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  return (
    <div 
      ref={modalRef}
      class="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4"
      onClick={handleBackdropClick}
    >
      <div class="bg-gray-800 rounded-lg shadow-xl max-w-4xl w-full max-h-[80vh] flex flex-col border border-gray-600">
        {/* Header */}
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-700 shrink-0">
          <h3 class="text-lg font-semibold text-gray-100">cURL Command</h3>
          <button 
            onClick={onClose}
            class="text-gray-400 hover:text-white transition-colors p-1"
          >
            ✕
          </button>
        </div>
        
        {/* Content - Scrollable */}
        <div class="flex-1 overflow-y-auto p-4 space-y-4 text-sm">
          {/* Proxy URL input */}
          <div>
            <label class="block text-gray-400 mb-1 text-xs">Proxy URL</label>
            <input
              type="text"
              value={proxyUrl}
              onInput={(e) => setProxyUrl((e.target as HTMLInputElement).value)}
              class="w-full bg-gray-900 border border-gray-600 rounded px-3 py-2 text-gray-200 text-sm font-mono focus:outline-none focus:border-blue-500"
              placeholder="http://localhost:8080/v1/chat/completions"
            />
          </div>

          {/* cURL command */}
          <div>
            <div class="flex items-center justify-between mb-1">
              <label class="block text-gray-400 text-xs">Command</label>
              <button
                onClick={handleCopy}
                class={`px-3 py-1 rounded text-xs font-medium transition-colors ${
                  copied 
                    ? 'bg-green-600 text-white' 
                    : 'bg-blue-600 hover:bg-blue-500 text-white'
                }`}
              >
                {copied ? '✓ Copied!' : 'Copy'}
              </button>
            </div>
            <pre class="text-gray-300 bg-gray-900 p-3 rounded text-xs overflow-x-auto border border-gray-700 font-mono whitespace-pre-wrap break-all">
              {escapeHtmlLight(curlCommand)}
            </pre>
          </div>

          {/* Note */}
          <div class="text-xs text-gray-500 border-t border-gray-700 pt-3">
            <p class="mb-1">💡 <strong>Note:</strong></p>
            <ul class="list-disc list-inside space-y-1 ml-2">
              <li>Replace <code class="text-yellow-400">YOUR_API_KEY</code> with your actual proxy API token</li>
              <li>Update the proxy URL if different from localhost</li>
              <li>The command reconstructs the request from stored data</li>
            </ul>
          </div>
        </div>
      </div>
    </div>
  );
}

// Modal for displaying advanced request info
function AdvancedInfoModal({ 
  detail, 
  onClose 
}: { 
  detail: RequestDetailType; 
  onClose: () => void;
}) {
  const modalRef = useRef<HTMLDivElement>(null);

  // Close on escape key
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [onClose]);

  // Close on backdrop click
  const handleBackdropClick = (e: MouseEvent) => {
    if (e.target === modalRef.current) onClose();
  };

  return (
    <div 
      ref={modalRef}
      class="fixed inset-0 bg-black/60 flex items-center justify-center z-50 p-4"
      onClick={handleBackdropClick}
    >
      <div class="bg-gray-800 rounded-lg shadow-xl max-w-4xl w-full max-h-[80vh] flex flex-col border border-gray-600">
        {/* Header */}
        <div class="flex items-center justify-between px-4 py-3 border-b border-gray-700 shrink-0">
          <h3 class="text-lg font-semibold text-gray-100">Request Details</h3>
          <button 
            onClick={onClose}
            class="text-gray-400 hover:text-white transition-colors p-1"
          >
            ✕
          </button>
        </div>
        
        {/* Content - Scrollable */}
        <div class="flex-1 overflow-y-auto p-4 space-y-4 text-sm">
          {/* Basic Info */}
          <div class="space-y-2">
            <div class="flex justify-between">
              <span class="text-gray-400">ID:</span>
              <span class="text-gray-200 font-mono text-xs">{escapeHtml(detail.id)}</span>
            </div>
            <div class="flex justify-between">
              <span class="text-gray-400">Status:</span>
              <span class={
                detail.status === 'completed' ? 'text-green-400' :
                  detail.status === 'failed' ? 'text-red-400' :
                    detail.status === 'running' ? 'text-yellow-400' :
                      'text-gray-200'
              }>
                {escapeHtml(detail.status)}
              </span>
            </div>
            <div class="flex justify-between">
              <span class="text-gray-400">Duration:</span>
              <span class="text-gray-200">{escapeHtml(detail.duration)}</span>
            </div>
            <div class="flex justify-between">
              <span class="text-gray-400">Streaming:</span>
              <span class={detail.is_stream ? 'text-blue-400' : 'text-gray-400'}>
                {detail.is_stream ? 'true' : 'false'}
              </span>
            </div>
            <div class="flex justify-between">
              <span class="text-gray-400">Retries:</span>
              <span class={detail.retries > 0 ? 'text-yellow-400' : 'text-gray-200'}>
                {detail.retries}
              </span>
            </div>
          </div>

          {/* Model Info */}
          <div class="border-t border-gray-700 pt-4 space-y-2">
            <h4 class="text-gray-300 font-medium">Model</h4>
            <div class="flex justify-between">
              <span class="text-gray-400">Requested:</span>
              <span class="text-gray-200">{escapeHtml(detail.model)}</span>
            </div>
            {detail.original_model && detail.original_model !== detail.model && (
              <div class="flex justify-between">
                <span class="text-gray-400">Original:</span>
                <span class="text-gray-300">{escapeHtml(detail.original_model)}</span>
              </div>
            )}
            {detail.fallback_used && detail.fallback_used.length > 0 && (
              <div>
                <span class="text-gray-400">Fallback Chain:</span>
                <div class="mt-1 flex flex-wrap gap-1">
                  {detail.fallback_used.map((m, i) => (
                    <span key={i} class="text-gray-300 bg-gray-700 px-2 py-0.5 rounded text-xs">
                      {escapeHtml(m)}
                    </span>
                  ))}
                </div>
              </div>
            )}
          </div>

          {/* Parameters */}
          {detail.parameters && Object.keys(detail.parameters).length > 0 && (
            <div class="border-t border-gray-700 pt-4">
              <h4 class="text-gray-300 font-medium mb-2">Parameters</h4>
              <div class="text-gray-300 bg-gray-900 p-3 rounded text-xs overflow-x-auto border border-gray-700 font-mono">
                <JsonViewer data={detail.parameters} />
              </div>
            </div>
          )}

          {/* Error */}
          {detail.error && (
            <div class="border-t border-gray-700 pt-4">
              <h4 class="text-red-400 font-medium mb-2">Error</h4>
              <pre class="text-red-300 bg-red-900/20 p-3 rounded text-xs overflow-x-auto border border-red-500/30">
                {escapeHtml(detail.error)}
              </pre>
            </div>
          )}
        </div>
      </div>
    </div>
  );
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
  const [showModal, setShowModal] = useState(false);
  const [showCurlModal, setShowCurlModal] = useState(false);
  const messagesContainerRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (messagesContainerRef.current) {
      messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight;
    }
  }, [detail]);

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
        {/* Header Grid - Primary Fields */}
        <div class="flex items-start justify-between gap-4">
          <div class="grid grid-cols-2 gap-x-4 gap-y-2 flex-1">
            <div>
              <span class="text-gray-400">ID:</span>{' '}
              <span class="text-gray-200 font-mono text-xs">{escapeHtml(detail.id.slice(0, 8))}...</span>
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
              {detail.original_model && detail.original_model !== detail.model && (
                <span class="text-yellow-500 text-xs ml-1">(fallback)</span>
              )}
            </div>
            <div>
              <span class="text-gray-400">Duration:</span>{' '}
              <span class="text-gray-200">{escapeHtml(detail.duration)}</span>
            </div>
          </div>
          {/* Info Button */}
          <div class="flex items-center gap-1">
            <button
              onClick={() => setShowCurlModal(true)}
              class="text-gray-400 hover:text-white transition-colors p-2 hover:bg-gray-700 rounded"
              title="View cURL command"
            >
              📋
            </button>
            <button
              onClick={() => setShowModal(true)}
              class="text-gray-400 hover:text-white transition-colors p-2 hover:bg-gray-700 rounded"
              title="View details"
            >
              ℹ️
            </button>
          </div>
        </div>

        {/* Error Box */}
        {detail.error && (
          <div class="mt-3 bg-red-900/30 border border-red-500/50 rounded p-3">
            <span class="text-red-400 font-semibold">Error: </span>
            <span class="text-red-300">{escapeHtml(detail.error)}</span>
          </div>
        )}
      </div>

      {/* Modal */}
      {showModal && <AdvancedInfoModal detail={detail} onClose={() => setShowModal(false)} />}
      {showCurlModal && <CurlModal detail={detail} onClose={() => setShowCurlModal(false)} />}

      {/* Messages - Scrollable */}
      <div
        ref={messagesContainerRef}
        class="flex-1 overflow-y-auto min-h-0 p-4 monitor-font text-sm"
      >
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
                <details 
                  class="ml-8 mr-0 mt-1"
                  open={expandedThoughts.has(index)}
                >
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
                  <div class="mt-2 p-3 bg-amber-950/40 border border-amber-500/30 rounded text-amber-200/90 text-xs">
                    <CollapsibleText text={message.thinking} role="assistant" />
                  </div>
                </details>
              )}

              {/* Tool Calls */}
              {message.tool_calls && message.tool_calls.length > 0 && (
                <div class="ml-8 mr-0 mt-2 space-y-2">
                  {message.tool_calls.map((toolCall, tcIndex) => (
                    <ToolCallDisplay key={toolCall.id || tcIndex} toolCall={toolCall} />
                  ))}
                </div>
              )}
            </div>
          ))}

        </div>
      </div>
    </div>
  );
}
