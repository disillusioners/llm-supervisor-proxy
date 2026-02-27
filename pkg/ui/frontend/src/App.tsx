import { useState, useCallback } from 'preact/hooks';
import { Header, RequestList, RequestDetail, EventLog, ConfigModal } from './components';
import { useRequests, useRequestDetail, useConfig, useModels, useEvents, useEventRefresh, useTokens } from './hooks';


export function App() {
  // UI State
  const [selectedRequestId, setSelectedRequestId] = useState<string | null>(null);
  const [autoScroll, setAutoScroll] = useState(true);
  const [showConfig, setShowConfig] = useState(false);

  // API hooks
  const { requests, loading: requestsLoading, refetch: refetchRequests } = useRequests();
  const { detail, loading: detailLoading } = useRequestDetail(selectedRequestId);
  const { config, updateConfig, refetch: refetchConfig } = useConfig();
  const { models, addModel, updateModel, deleteModel, refetch: refetchModels } = useModels();
  const { tokens, createToken, deleteToken, refetch: refetchTokens } = useTokens();
  const { displayedEvents, containerRef, clearEvents } = useEvents(selectedRequestId, autoScroll);

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
    clearEvents();
  }, [clearEvents]);

  const handleOpenConfig = useCallback(() => {
    setShowConfig(true);
    refetchConfig();
    refetchModels();
    refetchTokens();
  }, [refetchConfig, refetchModels, refetchTokens]);

  const handleCloseConfig = useCallback(() => {
    setShowConfig(false);
  }, []);

  return (
    <>
      {/* Header */}
      <Header onOpenConfig={handleOpenConfig} />

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
          <RequestDetail detail={detail} loading={detailLoading} />

          <EventLog
            events={displayedEvents}
            autoScroll={autoScroll}
            onToggleAutoScroll={handleToggleAutoScroll}
            onClear={handleClearEvents}
            containerRef={containerRef}
          />
        </div>
      </main>

      {/* Config Modal */}
      <ConfigModal
        isOpen={showConfig}
        onClose={handleCloseConfig}
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
    </>
  );
}
