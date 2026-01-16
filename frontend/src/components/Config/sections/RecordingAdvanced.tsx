import { Input } from '../../primitives/Input';
import type { RecordingSettings } from '../../../types/api';

interface RecordingAdvancedProps {
  data: RecordingSettings;
  onChange: (data: Partial<RecordingSettings>) => void;
}

export function RecordingAdvanced({ data, onChange }: RecordingAdvancedProps) {
  const isRecordingEnabled = data.continuousEnabled || data.eventEnabled;

  return (
    <div className="space-y-4">
      {!isRecordingEnabled && (
        <p className="text-sm text-text-tertiary bg-bg-subtle rounded p-3">
          Recording is disabled. Select a recording mode in Quick Setup to configure these settings.
        </p>
      )}

      <Input
        label="Save Directory"
        type="text"
        value={data.saveDirectory}
        onChange={(e) => onChange({ saveDirectory: e.target.value })}
        helper="Where to save recordings (e.g., ~/recordings)"
        disabled={!isRecordingEnabled}
      />

      <div className="grid grid-cols-2 gap-4">
        <Input
          label="Segment Duration"
          type="number"
          value={data.segmentDuration}
          onChange={(e) => onChange({ segmentDuration: parseInt(e.target.value, 10) || 5 })}
          helper="Minutes per file"
          disabled={!isRecordingEnabled}
        />
        <Input
          label="Retention Days"
          type="number"
          value={data.retentionDays}
          onChange={(e) => onChange({ retentionDays: parseInt(e.target.value, 10) || 7 })}
          helper="Days before auto-delete"
          disabled={!isRecordingEnabled}
        />
      </div>

      {data.eventEnabled && (
        <div className="grid grid-cols-2 gap-4">
          <Input
            label="Pre-Motion Buffer"
            type="number"
            value={data.preMotionBuffer}
            onChange={(e) => onChange({ preMotionBuffer: parseInt(e.target.value, 10) || 5 })}
            helper="Seconds before motion"
          />
          <Input
            label="Post-Motion Buffer"
            type="number"
            value={data.postMotionBuffer}
            onChange={(e) => onChange({ postMotionBuffer: parseInt(e.target.value, 10) || 10 })}
            helper="Seconds after motion"
          />
        </div>
      )}
    </div>
  );
}

RecordingAdvanced.displayName = 'RecordingAdvanced';
