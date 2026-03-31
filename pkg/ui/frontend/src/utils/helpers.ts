export function escapeHtml(text: string | undefined | null): string {
  if (!text) return '';
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

// Minimal escape for code/pre blocks - only escapes chars that break HTML
export function escapeHtmlLight(text: string | undefined | null): string {
  if (!text) return '';
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

export function formatTime(timestamp: number): string {
  return new Date(timestamp * 1000).toISOString().split('T')[1].split('.')[0];
}

export function formatLocaleTime(dateStr: string): string {
  return new Date(dateStr).toLocaleTimeString();
}

export function formatDuration(duration: string): string {
  if (!duration) return '';

  // Parse Go-style duration (e.g., "1m41.385559131s", "2.5s", "500ms")
  const totalMs = parseGoDuration(duration);
  if (totalMs === null) return duration;

  // Format in human-readable way
  return formatMilliseconds(totalMs);
}

function parseGoDuration(duration: string): number | null {
  let totalMs = 0;
  let remaining = duration;

  // Match patterns like "1h", "30m", "45s", "500ms"
  const unitMultipliers: Record<string, number> = {
    'h': 3600000,
    'm': 60000,
    's': 1000,
    'ms': 1,
  };

  // Process in order of longest unit first to handle compound durations
  const regex = /([\d.]+)(ms|h|m|s)/g;
  let match;
  let hasMatch = false;

  while ((match = regex.exec(remaining)) !== null) {
    hasMatch = true;
    const value = parseFloat(match[1]);
    const unit = match[2];
    totalMs += value * unitMultipliers[unit];
  }

  return hasMatch ? totalMs : null;
}

function formatMilliseconds(ms: number): string {
  if (ms < 1000) {
    return `${Math.round(ms)}ms`;
  }

  const seconds = Math.floor(ms / 1000);
  const minutes = Math.floor(seconds / 60);
  const hours = Math.floor(minutes / 60);

  if (hours > 0) {
    const remainMins = minutes % 60;
    const remainSecs = seconds % 60;
    if (remainMins > 0) {
      return `${hours}h ${remainMins}m`;
    }
    return `${hours}h`;
  }

  if (minutes > 0) {
    const remainSecs = seconds % 60;
    if (remainSecs > 0) {
      return `${minutes}m ${remainSecs}s`;
    }
    return `${minutes}m`;
  }

  // Less than a minute: show seconds with 1 decimal
  const sec = ms / 1000;
  const rounded = Math.round(sec * 10) / 10;
  return rounded % 1 === 0 ? `${rounded}s` : `${rounded}s`;
}

export function truncateId(id: string, length = 8): string {
  return id.substring(0, length) + '...';
}

export function generateCurlCommand(
  model: string,
  messages: Array<{ role: string; content: string; thinking?: string; tool_calls?: unknown[] }>,
  parameters?: Record<string, unknown>,
  stream?: boolean,
  proxyUrl?: string
): string {
  const url = proxyUrl || 'http://localhost:8080/v1/chat/completions';
  
  const body: Record<string, unknown> = {
    model,
    messages: messages.map(m => ({
      role: m.role,
      content: m.content,
      ...(m.thinking && { thinking: m.thinking }),
      ...(m.tool_calls && { tool_calls: m.tool_calls }),
    })),
    stream: stream ?? false,
    ...parameters,
  };
  
  const jsonBody = JSON.stringify(body, null, 2);
  
  return `curl -X POST '${url}' \\
  -H 'Content-Type: application/json' \\
  -H 'Authorization: Bearer YOUR_API_KEY' \\
  -d '${jsonBody}'`;
}

// Parse think tags from content and extract thinking
export interface ParsedContent {
  thinking: string[];  // Array of extracted think tag contents
  content: string;     // Content with think tags removed
}

const THINK_TAG_REGEX = /<think>([\s\S]*?)<\/think>/gi;

export function parseThinkTags(content: string | undefined | null): ParsedContent {
  if (!content) {
    return { thinking: [], content: '' };
  }

  const thinking: string[] = [];
  let match;

  // Extract all think tags
  while ((match = THINK_TAG_REGEX.exec(content)) !== null) {
    thinking.push(match[1].trim());
  }

  // Remove think tags from content
  const cleanContent = content.replace(THINK_TAG_REGEX, '').trim();

  return { thinking, content: cleanContent };
}

export function formatTokenCount(n: number): string {
  if (n >= 1_000_000_000) return (n / 1_000_000_000).toFixed(1) + 'B';
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M';
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K';
  return n.toString();
}

export function formatHourBucket(bucket: string): string {
  // "2026-03-30T14" → "Mar 30, 14:00"
  return bucket.replace(/(\d{4})-(\d{2})-(\d{2})T(\d{2})/, (_, _y, m, d, h) => {
    const months = ['Jan','Feb','Mar','Apr','May','Jun','Jul','Aug','Sep','Oct','Nov','Dec'];
    return months[parseInt(m)-1] + ' ' + parseInt(d) + ', ' + h + ':00';
  });
}
