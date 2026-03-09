import { useState, useCallback } from 'preact/hooks';
import { Router, route } from 'preact-router';
import { Header, RequestList, RequestDetail, EventLog, SettingsPage } from './components';
import { useRequests, useRequestDetail, useConfig, useModels, useEvents, useEventRefresh, useTokens } from './hooks';

export function App() {
  // API hooks - fetch data once at the top level
  const { requests, loading: requestsLoading, refetch: refetchRequests } = useRequests();
  const { config, updateConfig, refetch: refetchConfig } = useConfig();
  const { models, addModel, updateModel, deleteModel, refetch: refetchModels } = useModels();
  const { tokens, createToken, deleteToken, refetch: refetchTokens } = useTokens();

  return (
    <Router>
      <SettingsRoute
        path="/ui/settings"
        config={config}
        onUpdateConfig={updateConfig}
        models={models}
        onAddModel={addModel}
        onUpdateModel={updateModel}
        onDeleteModel={deleteModel}
        tokens={tokens}
        onCreateToken={createToken}
        onDeleteToken={deleteToken}
        onRefetchTokens={refetchTokens}
      />
      <DashboardRoute
        default
        path="/ui"
        requests={requests}
        requestsLoading={requestsLoading}
        refetchRequests={refetchRequests}
      />
    </Router>
  );
}

// Dashboard route component
function DashboardRoute({ 
  requests, 
  requestsLoading, 
  refetchRequests 
}: { 
  requests: any[];
  requestsLoading: boolean;
  refetchRequests: () => void;
}) {
  // Use a separate state for selected request ID in dashboard
  const [selectedRequestId, setSelectedRequestId] = useState<string | null>(null);
  const [autoScroll, setAutoScroll] = useState(true);
  
  // Get detail for selected request
  const { detail: selectedDetail, loading: selectedDetailLoading } = useRequestDetail(selectedRequestId);
  const { displayedEvents: selectedEvents, containerRef: selectedContainerRef, clearEvents: clearSelectedEvents } = useEvents(selectedRequestId, autoScroll);

  // Event refresh callback
  const handleEventRefresh = useCallback(() => {
    refetchRequests();
  }, [refetchRequests]);

  useEventRefresh(handleEventRefresh);

  // Handlers
  const handleSelectRequest = useCallback((id: string) => {
    setSelectedRequestId(id);
  }, []);

  const handleToggleAutoScroll = useCallback(() => {
    setAutoScroll((prev) => !prev);
  }, []);

  const handleClearEvents = useCallback(() => {
    clearSelectedEvents();
  }, [clearSelectedEvents]);

  return (
    <div class="h-screen flex flex-col bg-gray-900">
      {/* Header */}
      <Header />

      {/* Main Content Grid */}
      <main class="flex-1 grid grid-cols-12 gap-0 overflow-hidden">
        {/* Left Panel: Request List */}
        <RequestList
          requests={requests}
          selectedId={selectedRequestId}
          onSelect={handleSelectRequest}
          onRefresh={refetchRequests}
          loading={requestsLoading}
        />

        {/* Right Panel: Stacked Request Details and Event Log */}
        <div class="col-span-9 flex flex-col min-h-0">
          <RequestDetail detail={selectedDetail} loading={selectedDetailLoading} />

          <EventLog
            events={selectedEvents}
            autoScroll={autoScroll}
            onToggleAutoScroll={handleToggleAutoScroll}
            onClear={handleClearEvents}
            containerRef={selectedContainerRef}
          />
        </div>
      </main>
    </div>
  );
}

// Settings route component - wraps SettingsPage with data fetching
function SettingsRoute({ 
  config, 
  onUpdateConfig, 
  models, 
  onAddModel, 
  onUpdateModel, 
  onDeleteModel,
  tokens,
  onCreateToken,
  onDeleteToken,
  onRefetchTokens,
}: { 
  config: any;
  onUpdateConfig: (config: any) => Promise<any>;
  models: any[];
  onAddModel: (model: any) => Promise<void>;
  onUpdateModel: (id: string, updates: any) => Promise<void>;
  onDeleteModel: (id: string) => Promise<void>;
  tokens: any[];
  onCreateToken: (name: string, expiresAt: string | null) => Promise<any>;
  onDeleteToken: (id: string) => Promise<void>;
  onRefetchTokens: () => void;
}) {
  return (
    <SettingsPage
      config={config}
      onUpdateConfig={onUpdateConfig}
      models={models}
      onAddModel={onAddModel}
      onUpdateModel={onUpdateModel}
      onDeleteModel={onDeleteModel}
      tokens={tokens}
      onCreateToken={onCreateToken}
      onDeleteToken={onDeleteToken}
      onRefetchTokens={onRefetchTokens}
    />
  );
}
