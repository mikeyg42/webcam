import { useState } from 'react';
import { Play, Download, Trash2, Pencil, Check, X, Activity, Video } from 'lucide-react';
import { useRecordingsBrowserStore, type BrowseRecording } from '../../stores/recordingsBrowserStore';
import { apiClient } from '../../api/client';
import { Card } from '../layout/Card';
import { Badge } from '../primitives/Badge';
import { Button } from '../primitives/Button';

function formatDuration(seconds: number): string {
  if (!seconds || seconds <= 0) return '0s';
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const s = Math.floor(seconds % 60);
  if (h > 0) return `${h}h ${m}m ${s}s`;
  if (m > 0) return `${m}m ${s}s`;
  return `${s}s`;
}

function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`;
}

function formatDate(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  });
}

function getDisplayName(rec: BrowseRecording): string {
  if (rec.metadata?.name) return rec.metadata.name;
  const date = new Date(rec.started_at);
  const prefix = rec.type === 'event' ? 'Motion' : 'Recording';
  return `${prefix} - ${date.toLocaleDateString()} ${date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}`;
}

interface RecordingCardProps {
  recording: BrowseRecording;
}

export function RecordingCard({ recording }: RecordingCardProps) {
  const { selectRecording, deleteRecording, renameRecording } = useRecordingsBrowserStore();
  const [isEditing, setIsEditing] = useState(false);
  const [editName, setEditName] = useState('');
  const [confirmDelete, setConfirmDelete] = useState(false);

  const displayName = getDisplayName(recording);

  const handleStartEdit = () => {
    setEditName(displayName);
    setIsEditing(true);
  };

  const handleSaveEdit = async () => {
    if (editName.trim()) {
      await renameRecording(recording.id, editName.trim());
    }
    setIsEditing(false);
  };

  const handleDelete = async () => {
    await deleteRecording(recording.id);
    setConfirmDelete(false);
  };

  return (
    <Card padding="sm" className="hover:border-accent/30 transition-colors">
      <div className="flex items-start gap-4">
        {/* Info */}
        <div className="flex-1 min-w-0 space-y-1.5">
          {/* Name row */}
          <div className="flex items-center gap-2">
            {isEditing ? (
              <div className="flex items-center gap-1.5 flex-1">
                <input
                  type="text"
                  value={editName}
                  onChange={(e) => setEditName(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') handleSaveEdit();
                    if (e.key === 'Escape') setIsEditing(false);
                  }}
                  autoFocus
                  className="flex-1 px-2 py-1 text-sm bg-bg-primary border border-accent rounded text-text-primary outline-none"
                />
                <button onClick={handleSaveEdit} className="p-1 text-status-success hover:bg-bg-hover rounded">
                  <Check className="w-3.5 h-3.5" />
                </button>
                <button onClick={() => setIsEditing(false)} className="p-1 text-text-tertiary hover:bg-bg-hover rounded">
                  <X className="w-3.5 h-3.5" />
                </button>
              </div>
            ) : (
              <>
                <span className="text-sm font-medium text-text-primary truncate">{displayName}</span>
                <button onClick={handleStartEdit} className="p-1 text-text-tertiary hover:text-text-primary hover:bg-bg-hover rounded shrink-0">
                  <Pencil className="w-3 h-3" />
                </button>
              </>
            )}
          </div>

          {/* Metadata row */}
          <div className="flex items-center gap-3 text-xs text-text-tertiary">
            <Badge
              variant={recording.type === 'event' ? 'warning' : 'info'}
              size="sm"
            >
              {recording.type === 'event' ? (
                <><Activity className="w-3 h-3" /> Motion</>
              ) : (
                <><Video className="w-3 h-3" /> Manual</>
              )}
            </Badge>
            <span>{formatDate(recording.started_at)}</span>
            <span>{formatDuration(recording.duration_seconds)}</span>
            <span>{formatBytes(recording.total_size_bytes)}</span>
            {recording.status !== 'completed' && (
              <Badge variant={recording.status === 'recording' ? 'success' : 'default'} size="sm" dot={recording.status === 'recording'}>
                {recording.status}
              </Badge>
            )}
          </div>
        </div>

        {/* Actions */}
        <div className="flex items-center gap-1.5 shrink-0">
          <Button
            variant="ghost"
            size="sm"
            icon={<Play className="w-4 h-4" />}
            onClick={() => selectRecording(recording.id)}
          >
            Play
          </Button>
          <a href={apiClient.getDownloadUrl(recording.id)} download>
            <Button variant="ghost" size="sm" icon={<Download className="w-4 h-4" />}>
              Save
            </Button>
          </a>
          {confirmDelete ? (
            <div className="flex items-center gap-1">
              <Button variant="danger" size="sm" onClick={handleDelete}>
                Confirm
              </Button>
              <Button variant="ghost" size="sm" onClick={() => setConfirmDelete(false)}>
                Cancel
              </Button>
            </div>
          ) : (
            <Button
              variant="ghost"
              size="sm"
              icon={<Trash2 className="w-4 h-4" />}
              onClick={() => setConfirmDelete(true)}
            />
          )}
        </div>
      </div>
    </Card>
  );
}

RecordingCard.displayName = 'RecordingCard';
