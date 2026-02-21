export function escapeHtml(text: string | undefined | null): string {
  if (!text) return '';
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#039;');
}

export function formatTime(timestamp: number): string {
  return new Date(timestamp * 1000).toISOString().split('T')[1].split('.')[0];
}

export function formatLocaleTime(dateStr: string): string {
  return new Date(dateStr).toLocaleTimeString();
}

export function truncateId(id: string, length = 8): string {
  return id.substring(0, length) + '...';
}
