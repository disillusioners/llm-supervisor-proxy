import { useState, useRef, useEffect, useMemo, useCallback, useLayoutEffect, MutableRef } from 'preact/hooks';
import { memo } from 'preact/compat';

import DOMPurify from 'dompurify';
import { marked } from 'marked';

import type { RequestDetail as RequestDetailType } from '../types';
import { escapeHtml, escapeHtmlLight, generateCurlCommand, parseThinkTags } from '../utils/helpers';
import { buildOffsets, computeWindowedRange, isPinnedToBottom } from '../utils/windowing';

interface RequestDetailProps {
  detail: RequestDetailType | null;
  loading: boolean;
}

// Estimated average rendered height of a single message bubble. Used by
// the windowed list to (a) size the spacers and (b) seed offsets for
// indices that haven't been measured yet. The hook updates offsets from
// a per-bubble ResizeObserver, so once a bubble is measured its real
// height drives the window math — the estimate is only a fallback.
const WINDOW_MESSAGE_ESTIMATED_HEIGHT = 220;
const WINDOW_OVERSCAN = 6;
// "Near bottom" threshold (px) for the pinned-to-bottom predicate. We
// treat any scroll position within this many pixels of the maximum as
// pinned; the user has clearly indicated they want to be at the bottom
// and we keep them there across late height materialization.
const PIN_TO_BOTTOM_THRESHOLD = 16;

// Hand-rolled virtual list. The expensive parts of a message render
// (parseThinkTags + CollapsibleText with its markdown parse + DOMPurify)
// only run for messages in [start, end). For a 794-message detail that
// drops the initial render cost from "freeze the tab" to ~30 messages.
//
// The hook reads scrollTop + clientHeight off the live container on every
// scroll/resize tick and delegates the actual slice math to
// `computeWindowedRange` (which accepts a prefix-sum offsets array so
// real, measured bubble heights drive the math — not a uniform estimate).
//
// Also tracks a "pinned to bottom" flag the parent uses to decide
// whether to programmatically re-anchor scrollTop when heights change.
function useWindowedMessageRange(
  total: number,
  scrollRef: MutableRef<HTMLDivElement | null>,
  measuredOffsets: ReadonlyArray<number> | null,
): { start: number; end: number; pinnedToBottomRef: MutableRef<boolean> } {
  const [range, setRange] = useState(() => ({
    start: 0,
    end: Math.min(total, 20),
  }));
  const pinnedToBottomRef = useRef(false);

  useEffect(() => {
    if (total === 0) {
      setRange({ start: 0, end: 0 });
      return;
    }

    const el = scrollRef.current;
    if (!el) return;

    let raf = 0;
    const update = () => {
      raf = 0;
      const { scrollTop, clientHeight, scrollHeight } = el;
      const { start: first, end: last } = computeWindowedRange(
        scrollTop,
        clientHeight,
        total,
        WINDOW_MESSAGE_ESTIMATED_HEIGHT,
        WINDOW_OVERSCAN,
        measuredOffsets ?? undefined,
      );
      pinnedToBottomRef.current = isPinnedToBottom(scrollTop, clientHeight, scrollHeight, PIN_TO_BOTTOM_THRESHOLD);
      setRange((prev) =>
        prev.start === first && prev.end === last ? prev : { start: first, end: last },
      );
    };

    const onScroll = () => {
      if (raf) return;
      raf = requestAnimationFrame(update);
    };

    update();
    el.addEventListener('scroll', onScroll, { passive: true });

    const ro = new ResizeObserver(update);
    ro.observe(el);

    return () => {
      el.removeEventListener('scroll', onScroll);
      if (raf) cancelAnimationFrame(raf);
      ro.disconnect();
    };
  }, [scrollRef, total, measuredOffsets]);

  return { start: range.start, end: range.end, pinnedToBottomRef };
}

// Memoized markdown parser cache with LRU eviction
const markdownCache = new Map<string, string>();
const MAX_CACHE_SIZE = 100;

function getCachedHtml(text: string): string {
  const cached = markdownCache.get(text);
  if (cached !== undefined) {
    // Move to end (most recently used) - Map maintains insertion order
    markdownCache.delete(text);
    markdownCache.set(text, cached);
    return cached;
  }

  if (markdownCache.size >= MAX_CACHE_SIZE) {
    // Remove oldest (first) entry
    const oldest = markdownCache.keys().next().value;
    if (oldest) markdownCache.delete(oldest);
  }

  const html = DOMPurify.sanitize(marked.parse(text, { async: false }) as string);
  markdownCache.set(text, html);
  return html;
}

const MarkdownContent = memo(function MarkdownContent({ text }: { text: string }) {
  const html = getCachedHtml(text);
  return (
    <div
      class="prose prose-invert prose-sm max-w-none"
      dangerouslySetInnerHTML={{ __html: html }}
    />
  );
});

// Lazy wrapper around MarkdownContent. The expensive marked.parse +
// DOMPurify.sanitize only runs when the element is within ~1.5 viewports
// of the visible area; otherwise a tiny placeholder occupies the slot so
// the bubble's vertical footprint stays predictable. This is what keeps a
// 794-message conversation from blocking the main thread on initial mount.
const LazyMarkdownContent = memo(function LazyMarkdownContent({ text }: { text: string }) {
  const ref = useRef<HTMLDivElement | null>(null);
  const active = useNearViewport(ref, '1500px 0px');

  if (!active) {
    // Use the windowing estimated row height as the placeholder's
    // minimum so that, once the row enters the visible window and
    // activates, the bubble doesn't dramatically resize and shift
    // surrounding messages. Subtract ~24 px to compensate for the
    // bubble's `p-3` padding (12 px each side) so the bubble's OUTER
    // height is roughly WINDOW_MESSAGE_ESTIMATED_HEIGHT — close to
    // what windowing's spacer math assumes.
    const placeholderMinHeight = Math.max(32, WINDOW_MESSAGE_ESTIMATED_HEIGHT - 24);
    return (
      <div
        ref={ref}
        class="text-xs text-gray-600 italic select-none"
        style={{ minHeight: `${placeholderMinHeight}px` }}
        aria-hidden="true"
      >
        …
      </div>
    );
  }

  return <MarkdownContent text={text} />;
});

// Returns true once the ref'd element is within ~rootMargin of the
// viewport. Sticky (never goes back to false) — once mounted and parsed,
// a message stays parsed even if the user scrolls it offscreen again.
// This avoids flicker on re-entry.
function useNearViewport(
  ref: MutableRef<HTMLDivElement | null>,
  rootMargin: string,
): boolean {
  const [near, setNear] = useState(false);

  useEffect(() => {
    const el = ref.current;
    if (!el) return;

    // Cheap fast-path: if the element is already near the viewport on
    // mount (typical for the first ~20 messages of a freshly-loaded
    // detail), activate immediately without an observer.
    const rect = el.getBoundingClientRect();
    const viewportH = typeof window !== 'undefined' ? window.innerHeight : 800;
    if (rect.top < viewportH * 2 && rect.bottom > -200) {
      setNear(true);
      return;
    }

    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) {
            setNear(true);
            observer.disconnect();
            return;
          }
        }
      },
      { rootMargin },
    );
    observer.observe(el);
    return () => observer.disconnect();
  }, [ref, rootMargin]);

  return near;
}

// Tool Call Display Component - Collapsible with formatted arguments
const ToolCallDisplay = memo(function ToolCallDisplay({ toolCall }: { toolCall: { function: { name: string; arguments: string } } }) {
  const [isExpanded, setIsExpanded] = useState(false);

  // Parse arguments with useMemo to avoid double-render from useEffect
  const { parsedArgs, parseError } = useMemo(() => {
    try {
      return { parsedArgs: JSON.parse(toolCall.function.arguments), parseError: false };
    } catch {
      return { parsedArgs: null, parseError: true };
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
});

// Collapsible JSON viewer component
const JsonViewer = memo(function JsonViewer({
  data,
  depth = 0,
  defaultExpanded = true,
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
});

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

const CollapsibleText = memo(function CollapsibleText({ text, role }: { text: string; role?: string }) {
  const [isExpanded, setIsExpanded] = useState(false);
  const lines = text ? text.split('\n') : [];

  if (lines.length <= 40) {
    return <LazyMarkdownContent text={text} />;
  }

  if (isExpanded) {
    return (
      <div class="flex flex-col">
        <LazyMarkdownContent text={text} />
        <button
          onClick={() => setIsExpanded(false)}
          class={`mt-3 self-center text-xs px-3 py-1 rounded border transition-colors ${
            role === 'user'
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
        <LazyMarkdownContent text={firstHalf} />
      </div>

      <div class="flex items-center justify-center my-3 opacity-80 hover:opacity-100 transition-opacity">
        <div class={`h-px flex-1 ${role === 'user' ? 'bg-gray-600' : 'bg-blue-800/50'}`}></div>
        <button
          onClick={() => setIsExpanded(true)}
          class={`mx-3 text-xs px-3 py-1 rounded border transition-colors flex-shrink-0 ${
            role === 'user'
              ? 'text-gray-300 hover:text-white bg-gray-600/50 border-gray-500/50'
              : 'text-blue-400 hover:text-blue-300 bg-blue-900/20 border-blue-500/30'
          }`}
        >
          ... Show {hiddenCount} hidden lines ...
        </button>
        <div class={`h-px flex-1 ${role === 'user' ? 'bg-gray-600' : 'bg-blue-800/50'}`}></div>
      </div>

      <div class="relative overflow-hidden opacity-75">
        <LazyMarkdownContent text={secondHalf} />
      </div>
    </div>
  );
});

// Wrapper that observes its own height via ResizeObserver and reports
// any change to the parent's height map. Memoized so the underlying
// message bubble only re-renders when the parent passes new content
// (not when the height map updates). Keys off `index` so React reconciles
// the same wrapper across scrolls in/out of the windowed range.
const MeasuredBubble = memo(function MeasuredBubble({
  index,
  onMeasure,
  children,
}: {
  index: number;
  onMeasure: (index: number, height: number) => void;
  children: preact.ComponentChildren;
}) {
  const ref = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let lastHeight = -1;
    const report = (h: number) => {
      if (h > 0 && Math.abs(h - lastHeight) > 0.5) {
        lastHeight = h;
        onMeasure(index, h);
      }
    };
    // Report immediately for the initial layout — ResizeObserver fires on
    // mutation but not on the first paint, and we want the offsets array
    // populated as soon as the bubble is in the DOM.
    report(el.getBoundingClientRect().height);
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        report(entry.contentRect.height);
      }
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [index, onMeasure]);
  return (
    <div ref={ref} data-message-index={index}>
      {children}
    </div>
  );
});

export function RequestDetail({ detail, loading }: RequestDetailProps) {
  const [expandedThoughts, setExpandedThoughts] = useState<Set<number | string>>(new Set());
  const [showModal, setShowModal] = useState(false);
  const [showCurlModal, setShowCurlModal] = useState(false);
  const messagesContainerRef = useRef<HTMLDivElement>(null);
  const totalMessages = detail?.messages?.length ?? 0;
  // Per-message measured height map. Index → height-in-px. Populated by
  // MeasuredBubble's ResizeObserver; consumed by `buildOffsets` to seed
  // the prefix-sum array the windowed hook reads.
  const [heights, setHeights] = useState<Map<number, number>>(() => new Map());
  const handleBubbleMeasure = useCallback((index: number, height: number) => {
    setHeights((prev) => {
      const existing = prev.get(index);
      if (existing === height) return prev;
      const next = new Map(prev);
      next.set(index, height);
      return next;
    });
  }, []);
  const offsets = useMemo(
    () => buildOffsets(totalMessages, heights, WINDOW_MESSAGE_ESTIMATED_HEIGHT),
    [totalMessages, heights],
  );
  const { start, end, pinnedToBottomRef } = useWindowedMessageRange(
    totalMessages,
    messagesContainerRef,
    totalMessages > 0 ? offsets : null,
  );
  const windowedRange = { start, end };

  // Anchor to the bottom on detail change. Deferred to the next animation
  // frame so any test-time layout shim that mutates scrollHeight AFTER
  // the render commit is visible to the read. In production the
  // one-frame delay between paint and the anchor is imperceptible
  // compared to the round-trip cost of clicking a new request.
  //
  // Also marks the position as pinned so the offsets-change effect below
  // keeps us at the bottom as late height materialization finishes.
  //
  // Programmatic scrollTop assignment does NOT fire a 'scroll' event, so
  // without dispatching one the windowing hook's scroll listener would
  // never see this anchor and the user would land on the bottom SPACER
  // (empty) instead of the last message.
  useEffect(() => {
    // On cross-request switch (A → B), reset the per-message height map
    // BEFORE scheduling the rAF anchor. Otherwise A's per-index
    // measurements leak into B's offsets for ≥1 frame, the detail-change
    // rAF anchors against B's `scrollHeight` polluted by A's stale
    // prefix sums, and the `existing === height` short-circuit inside
    // `handleBubbleMeasure` prolongs the pollution (no-op setHeights
    // returns the same Map identity, so the `offsets` useMemo never
    // recomputes). Clearing `pinnedToBottomRef.current` synchronously
    // also stops the useLayoutEffect that fires on the immediate
    // heights-driven `offsets` recomputation from programmatically
    // re-anchoring against the still-estimating scrollHeight — the rAF
    // below is the single, deterministic anchor.
    setHeights(() => new Map());
    pinnedToBottomRef.current = false;
    const rafId = requestAnimationFrame(() => {
      const el = messagesContainerRef.current;
      if (!el) return;
      pinnedToBottomRef.current = true;
      el.scrollTop = el.scrollHeight;
      el.dispatchEvent(new Event('scroll'));
    });
    return () => cancelAnimationFrame(rafId);
  }, [detail, pinnedToBottomRef]);

  // Re-anchor when the height map changes and the user is pinned to the
  // bottom. This is what survives the "tall last message materializes
  // after initial anchor" case: as each bubble's real height comes in,
  // scrollHeight grows, and we slide scrollTop to the new maximum.
  //
  // Critical: we only touch scrollTop when pinnedToBottomRef is true. If
  // the user has scrolled away from the bottom, we never programmatically
  // move them — that's the user's territory.
  useLayoutEffect(() => {
    const el = messagesContainerRef.current;
    if (!el || totalMessages === 0) return;
    if (pinnedToBottomRef.current) {
      el.scrollTop = el.scrollHeight - el.clientHeight;
    }
  }, [offsets, pinnedToBottomRef, totalMessages]);

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

  // Jump helpers — for 800-message logs, "scroll to bottom" via the
  // scrollbar alone is unreliable (spacer-based windowing). These buttons
  // compute scrollTop directly from message index, using the real prefix-
  // sum offsets so a very tall last message lands correctly instead of
  // overshooting into the spacer.
  const jumpToMessage = useCallback((targetIndex: number) => {
    const el = messagesContainerRef.current;
    if (!el) return;
    const clamped = Math.max(0, Math.min(targetIndex, totalMessages - 1));
    // offsets[i] is the y-offset of the top of message i. For clamped,
    // use the real offset when available; otherwise fall back to the
    // uniform estimate so the helper still works before any bubble has
    // been measured.
    const topOffset = offsets[clamped] ?? clamped * WINDOW_MESSAGE_ESTIMATED_HEIGHT;
    el.scrollTop = Math.max(0, topOffset - el.clientHeight / 2);
    // Programmatic scrollTop assignment does NOT fire a 'scroll' event,
    // so without dispatching one the windowing hook's scroll listener
    // would never wake up and the visible slice would stay stale even
    // though the scrollbar moved — a regression of shipped UI behaviour
    // (the '⤒ First' / '⤓ Last' buttons must actually move the window).
    // Mirror the detail-change effect's pattern at the top of this
    // component: assign scrollTop, then synthesise the event.
    el.dispatchEvent(new Event('scroll'));
  }, [totalMessages, offsets]);

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

      {/* Messages - Scrollable, windowed for large conversations.
          overflowAnchor is explicitly disabled because the component
          manages scroll anchoring itself via the useLayoutEffect above;
          letting the browser auto-anchor would fight the windowing math
          whenever a measured bubble resizes. */}
      <div
        ref={messagesContainerRef}
        class="flex-1 overflow-y-auto min-h-0 p-4 monitor-font text-sm"
        style={{ overflowAnchor: 'none' }}
      >
        {/* Jump nav for long conversations (≥ 50 messages). Uses real
            measured offsets as the scroll proxy where available — the
            scrollbar is itself inaccurate under windowing, so users
            need explicit anchors. */}
        {totalMessages >= 50 && (
          <div class="mb-3 flex items-center justify-between gap-2 text-xs">
            <span class="text-gray-500">
              Showing {Math.min(windowedRange.start + 1, Math.min(windowedRange.end, totalMessages))}–{Math.min(windowedRange.end, totalMessages)} of {totalMessages} messages
            </span>
            <div class="flex items-center gap-1">
              <button
                onClick={() => jumpToMessage(0)}
                class="px-2 py-1 rounded bg-gray-700 hover:bg-gray-600 text-gray-200"
                title="Jump to first message"
              >
                ⤒ First
              </button>
              <button
                onClick={() => jumpToMessage(Math.max(0, windowedRange.start - 20))}
                class="px-2 py-1 rounded bg-gray-700 hover:bg-gray-600 text-gray-200"
                title="Jump back 20 messages"
              >
                −20
              </button>
              <button
                onClick={() => jumpToMessage(Math.min(totalMessages - 1, windowedRange.end + 20))}
                class="px-2 py-1 rounded bg-gray-700 hover:bg-gray-600 text-gray-200"
                title="Jump forward 20 messages"
              >
                +20
              </button>
              <button
                onClick={() => jumpToMessage(totalMessages - 1)}
                class="px-2 py-1 rounded bg-gray-700 hover:bg-gray-600 text-gray-200"
                title="Jump to last message"
              >
                ⤓ Last
              </button>
            </div>
          </div>
        )}

        {/* Spacer above the visible window — keeps scroll height realistic.
            Sized from the real prefix-sum offsets so a tall last message
            no longer makes this spacer undersized (the original "uniform
            estimate" bug). */}
        {windowedRange.start > 0 && (
          <div
            style={{ height: `${offsets[windowedRange.start] ?? windowedRange.start * WINDOW_MESSAGE_ESTIMATED_HEIGHT}px` }}
            aria-hidden="true"
          />
        )}
        <div class="space-y-3">
          {detail.messages.slice(windowedRange.start, windowedRange.end).map((message, idx) => {
            const index = windowedRange.start + idx;
            // Parse inline think tags for assistant messages
            const parsed = message.role === 'assistant'
              ? parseThinkTags(message.content)
              : { thinking: [], content: message.content };

            return (
              <MeasuredBubble key={index} index={index} onMeasure={handleBubbleMeasure}>
                {/* Inline Think Tags - Visually distinct from separate thinking field */}
                {parsed.thinking.length > 0 && (
                  <div class="ml-8 mr-0 mt-1 space-y-2">
                    {parsed.thinking.map((thinkContent, thinkIndex) => (
                      <details
                        key={thinkIndex}
                        class="bg-slate-800/50 border border-slate-500/30 rounded-lg overflow-hidden"
                        open={expandedThoughts.has(`inline-${index}-${thinkIndex}`)}
                      >
                        <summary
                          class="cursor-pointer text-xs text-slate-400 hover:text-slate-300 flex items-center gap-2 p-2 bg-slate-800/70"
                          onClick={(e) => {
                            e.preventDefault();
                            const key = `inline-${index}-${thinkIndex}`;
                            if (expandedThoughts.has(key)) {
                              expandedThoughts.delete(key);
                            } else {
                              expandedThoughts.add(key);
                            }
                            setExpandedThoughts(new Set(expandedThoughts));
                          }}
                        >
                          <span class={`transform transition-transform ${expandedThoughts.has(`inline-${index}-${thinkIndex}`) ? 'rotate-90' : ''}`}>
                            ▶
                          </span>
                          <span class="text-slate-500">[</span>
                          <span class="flex items-center gap-1">
                            <span>💭</span>
                            <span>reasoning</span>
                          </span>
                          <span class="text-slate-500">]</span>
                        </summary>
                        <div class="p-3 text-xs text-slate-300/80 font-mono whitespace-pre-wrap">
                          <CollapsibleText text={thinkContent} role="assistant" />
                        </div>
                      </details>
                    ))}
                  </div>
                )}

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
                    <CollapsibleText text={parsed.content} role={message.role} />
                  </div>
                </div>

                {/* Separate Thinking Field - Distinct styling from inline tags */}
                {message.thinking && (
                  <details
                    class="ml-8 mr-0 mt-1"
                    open={expandedThoughts.has(index)}
                  >
                    <summary
                      class="cursor-pointer text-xs text-amber-400 hover:text-amber-300 flex items-center gap-1"
                      onClick={(e) => {
                        e.preventDefault();
                        toggleThought(index);
                      }}
                    >
                      <span class={`transform transition-transform ${expandedThoughts.has(index) ? 'rotate-90' : ''}`}>
                        ▶
                      </span>
                      <span>🧠</span>
                      <span>Thinking</span>
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
              </MeasuredBubble>
            );
          })}

        </div>
        {/* Spacer below the visible window — keeps scroll height realistic.
            (total scroll height) - (top of first not-rendered row) =
            offset[total] - offset[end]. Falls back to the uniform
            estimate if offsets aren't ready yet (very first render). */}
        {windowedRange.end < totalMessages && (
          <div
            style={{
              height: `${(offsets[totalMessages] ?? totalMessages * WINDOW_MESSAGE_ESTIMATED_HEIGHT) - (offsets[windowedRange.end] ?? windowedRange.end * WINDOW_MESSAGE_ESTIMATED_HEIGHT)}px`,
            }}
            aria-hidden="true"
          />
        )}
      </div>
    </div>
  );
}
