// API Types - matching Go backend structures

export interface ProxyConfig {
  UpstreamURL: string;
  IdleTimeout: number; // nanoseconds
  MaxGenerationTime: number; // nanoseconds
  MaxRetries: number;
}

export interface Model {
  id: string;
  name: string;
  enabled: boolean;
  fallback_chain: string[];
}

export interface ModelsResponse {
  models: Model[];
}

export interface Message {
  role: 'user' | 'assistant' | 'system';
  content: string;
  thinking?: string;
  tool_calls?: ToolCall[];
}

export interface ToolCall {
  id: string;
  type: 'function';
  function: {
    name: string;
    arguments: string;
  };
}

export interface Request {
  id: string;
  model: string;
  status: 'running' | 'completed' | 'failed' | 'retrying';
  startTime: string;
  duration: string;
  retries: number;
  error?: string;
}

export interface RequestDetail extends Request {
  messages: Message[];
  response?: string;
}

export type EventType =
  | 'request_started'
  | 'request_completed'
  | 'retry_attempt'
  | 'error_max_retries'
  | 'timeout_idle'
  | 'error';

export interface EventData {
  id?: string;
  timeout?: string;
  attempt?: number;
  error?: string;
}

export interface Event {
  type: EventType;
  timestamp: number;
  data: EventData | null;
}

// UI State Types
export interface AppState {
  selectedRequestId: string | null;
  autoScroll: boolean;
  showConfig: boolean;
}
