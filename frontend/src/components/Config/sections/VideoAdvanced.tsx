import { Input } from '../../primitives/Input';
import type { VideoSettings } from '../../../types/api';

interface VideoAdvancedProps {
  data: VideoSettings;
  onChange: (data: Partial<VideoSettings>) => void;
}

export function VideoAdvanced({ data, onChange }: VideoAdvancedProps) {
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-2 gap-4">
        <Input
          label="Framerate"
          type="number"
          value={data.framerate}
          onChange={(e) => onChange({ framerate: parseInt(e.target.value, 10) || 30 })}
          helper="Frames per second (e.g., 30)"
        />
        <Input
          label="Bitrate"
          type="number"
          value={data.bitRate}
          onChange={(e) => onChange({ bitRate: parseInt(e.target.value, 10) || 2000000 })}
          helper="Bits per second (e.g., 2000000)"
        />
      </div>
      <p className="text-xs text-text-tertiary">
        Resolution is auto-detected from camera
      </p>
    </div>
  );
}

VideoAdvanced.displayName = 'VideoAdvanced';
