import { useState, useCallback, useRef } from 'preact/hooks';
import { Header, RequestList, RequestDetail, EventLog, ConfigModal } from './components';
import { useRequests, useRequestDetail, useConfig, useModels, useEvents, useEventRefresh } from './hooks';
import type { Event } from './types';

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
  const { displayedEvents, containerRef, clearEvents } = useEvents(selectedRequestId, autoScroll);

  // Events map for tracking
  const eventsMapRef = useRef<Record<string, Event[]>>({});

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
  }, [refetchConfig, refetchModels]);

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

        {/* Middle Panel: Request Details */}
        <RequestDetail detail={detail} loading={detailLoading} />

        {/* Right Panel: Event Log */}
        <EventLog
          events={displayedEvents}
          autoScroll={autoScroll}
          onToggleAutoScroll={handleToggleAutoScroll}
          onClear={handleClearEvents}
          containerRef={containerRef}
        />
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
      />
    </>
  );
}
