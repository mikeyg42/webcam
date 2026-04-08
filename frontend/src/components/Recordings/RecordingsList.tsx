import { useEffect } from 'react';
import { motion } from 'motion/react';
import { Film, Loader2, Inbox } from 'lucide-react';
import { useRecordingsBrowserStore } from '../../stores/recordingsBrowserStore';
import { RecordingCard } from './RecordingCard';
import { RecordingPlayer } from './RecordingPlayer';
import { Select } from '../primitives/Select';
import { staggerContainer, staggerItem, slideUp } from '../../lib/motion';

export function RecordingsList() {
  const {
    recordings,
    selectedRecording,
    typeFilter,
    isLoading,
    error,
    fetchRecordings,
    clearSelection,
    setTypeFilter,
  } = useRecordingsBrowserStore();

  useEffect(() => {
    fetchRecordings();
  }, [fetchRecordings]);

  if (selectedRecording) {
    return <RecordingPlayer recording={selectedRecording} onBack={clearSelection} />;
  }

  return (
    <motion.div variants={slideUp} initial="hidden" animate="visible" className="space-y-6">
      {/* Header + Filters */}
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-3">
          <Film className="w-5 h-5 text-accent" />
          <h2 className="text-lg font-semibold text-text-primary">Recordings</h2>
        </div>
        <div className="w-48">
          <Select
            value={typeFilter}
            onChange={(e) => setTypeFilter(e.target.value)}
            options={[
              { value: '', label: 'All types' },
              { value: 'continuous', label: 'Manual' },
              { value: 'event', label: 'Motion event' },
            ]}
          />
        </div>
      </div>

      {/* Error */}
      {error && (
        <div className="p-3 bg-status-error/10 border border-status-error/30 rounded text-sm text-status-error">
          {error}
        </div>
      )}

      {/* Loading */}
      {isLoading && (
        <div className="flex items-center justify-center py-12">
          <Loader2 className="w-6 h-6 text-accent animate-spin" />
        </div>
      )}

      {/* Empty state */}
      {!isLoading && recordings.length === 0 && (
        <div className="flex flex-col items-center justify-center py-16 text-text-tertiary space-y-3">
          <Inbox className="w-12 h-12" />
          <p className="text-sm">No recordings yet</p>
          <p className="text-xs">Start a recording from the Camera tab</p>
        </div>
      )}

      {/* Recording list */}
      {!isLoading && recordings.length > 0 && (
        <motion.div
          variants={staggerContainer}
          initial="hidden"
          animate="visible"
          className="space-y-3"
        >
          {recordings.map((rec) => (
            <motion.div key={rec.id} variants={staggerItem}>
              <RecordingCard recording={rec} />
            </motion.div>
          ))}
        </motion.div>
      )}
    </motion.div>
  );
}

RecordingsList.displayName = 'RecordingsList';
