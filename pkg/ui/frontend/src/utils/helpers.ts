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
  const match = duration.match(/^([\d.]+)(s|ms|m|h)$/);
  if (!match) return duration;

  const value = parseFloat(match[1]);
  const unit = match[2];

  // Round to 1 decimal place
  const rounded = Math.round(value * 10) / 10;

  // Remove trailing .0 for whole numbers
  return rounded % 1 === 0 ? `${rounded}${unit}` : `${rounded}${unit}`;
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
