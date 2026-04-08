import { useState, useRef, useCallback } from 'react';
import { motion } from 'motion/react';
import { ArrowLeft, Download } from 'lucide-react';
import { type BrowseRecording } from '../../stores/recordingsBrowserStore';
import { apiClient } from '../../api/client';
import { Button } from '../primitives/Button';
import { Badge } from '../primitives/Badge';
import { slideUp } from '../../lib/motion';

function formatDuration(seconds: number): string {
  if (!seconds || seconds <= 0) return '0:00';
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  return `${m}:${s.toString().padStart(2, '0')}`;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

interface RecordingPlayerProps {
  recording: BrowseRecording;
  onBack: () => void;
}

export function RecordingPlayer({ recording, onBack }: RecordingPlayerProps) {
  const videoRef = useRef<HTMLVideoElement>(null);
  const [currentSegment, setCurrentSegment] = useState(0);
  const segments = recording.segments || [];
  const totalSegments = segments.length;

  const currentUrl = totalSegments > 0
    ? apiClient.getSegmentStreamUrl(recording.id, segments[currentSegment].index)
    : '';

  const handleEnded = useCallback(() => {
    if (currentSegment < totalSegments - 1) {
      setCurrentSegment((prev) => prev + 1);
    }
  }, [currentSegment, totalSegments]);

  const displayName = recording.metadata?.name ||
    `${recording.type === 'event' ? 'Motion' : 'Recording'} - ${new Date(recording.started_at).toLocaleString()}`;

  return (
    <motion.div variants={slideUp} initial="hidden" animate="visible" className="space-y-4">
      {/* Header */}
      <div className="flex items-center gap-3">
        <Button variant="ghost" size="sm" icon={<ArrowLeft className="w-4 h-4" />} onClick={onBack}>
          Back
        </Button>
        <h2 className="text-lg font-semibold text-text-primary truncate">{displayName}</h2>
      </div>

      {/* Video Player */}
      <div className="relative w-full aspect-video bg-black rounded-lg overflow-hidden border border-border">
        {totalSegments > 0 ? (
          <video
            ref={videoRef}
            key={currentSegment}
            src={currentUrl}
            autoPlay
            controls
            playsInline
            onEnded={handleEnded}
            className="w-full h-full object-contain"
          />
        ) : (
          <div className="flex items-center justify-center h-full text-text-tertiary text-sm">
            No segments available
          </div>
        )}
      </div>

      {/* Segments play sequentially — no user-visible segment controls */}

      {/* Recording Info */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3 text-sm">
        <InfoItem label="Type" value={
          <Badge variant={recording.type === 'event' ? 'warning' : 'info'} size="sm">
            {recording.type === 'event' ? 'Motion event' : 'Manual'}
          </Badge>
        } />
        <InfoItem label="Recorded" value={new Date(recording.started_at).toLocaleString()} />
        <InfoItem label="Duration" value={formatDuration(recording.duration_seconds)} />
        <InfoItem label="Size" value={formatBytes(recording.total_size_bytes)} />
        <InfoItem label="Status" value={recording.status} />
        {recording.codec?.Valid && (
          <InfoItem label="Codec" value={recording.codec.String} />
        )}
        {recording.resolution?.Valid && (
          <InfoItem label="Resolution" value={recording.resolution.String} />
        )}
      </div>

      {/* Download */}
      <div className="flex justify-end">
        <a href={apiClient.getDownloadUrl(recording.id)} download>
          <Button variant="secondary" icon={<Download className="w-4 h-4" />}>
            Download Recording
          </Button>
        </a>
      </div>
    </motion.div>
  );
}

function InfoItem({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <div className="text-xs text-text-tertiary uppercase tracking-label">{label}</div>
      <div className="text-text-primary">{typeof value === 'string' ? value : value}</div>
    </div>
  );
}

RecordingPlayer.displayName = 'RecordingPlayer';
