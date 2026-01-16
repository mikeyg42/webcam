import { Input } from '../../primitives/Input';
import { Select } from '../../primitives/Select';
import type { AudioSettings } from '../../../types/api';

interface AudioAdvancedProps {
  data: AudioSettings;
  onChange: (data: Partial<AudioSettings>) => void;
}

export function AudioAdvanced({ data, onChange }: AudioAdvancedProps) {
  const channelOptions = [
    { value: '1', label: 'Mono (1 channel)' },
    { value: '2', label: 'Stereo (2 channels)' },
  ];

  const sampleRateOptions = [
    { value: '16000', label: '16 kHz' },
    { value: '44100', label: '44.1 kHz' },
    { value: '48000', label: '48 kHz' },
  ];

  return (
    <div className="space-y-4">
      {!data.enabled && (
        <p className="text-sm text-text-tertiary bg-bg-subtle rounded p-3">
          Audio is disabled. Enable it in Quick Setup to configure these settings.
        </p>
      )}

      <div className="grid grid-cols-2 gap-4">
        <Select
          label="Sample Rate"
          options={sampleRateOptions}
          value={String(data.sampleRate)}
          onChange={(e) => onChange({ sampleRate: parseInt(e.target.value, 10) })}
          disabled={!data.enabled}
        />
        <Select
          label="Channels"
          options={channelOptions}
          value={String(data.channels)}
          onChange={(e) => onChange({ channels: parseInt(e.target.value, 10) })}
          disabled={!data.enabled}
        />
      </div>

      <Input
        label="Bitrate"
        type="number"
        value={data.bitRate}
        onChange={(e) => onChange({ bitRate: parseInt(e.target.value, 10) || 128000 })}
        helper="Audio bitrate in bits per second"
        disabled={!data.enabled}
      />
    </div>
  );
}

AudioAdvanced.displayName = 'AudioAdvanced';
