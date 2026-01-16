import { Input } from '../../primitives/Input';
import type { MotionSettings } from '../../../types/api';

interface MotionAdvancedProps {
  data: MotionSettings;
  onChange: (data: Partial<MotionSettings>) => void;
}

export function MotionAdvanced({ data, onChange }: MotionAdvancedProps) {
  return (
    <div className="space-y-4">
      {!data.enabled && (
        <p className="text-sm text-text-tertiary bg-bg-subtle rounded p-3">
          Motion detection is disabled. Enable it in Quick Setup to configure these settings.
        </p>
      )}

      <div className="grid grid-cols-2 gap-4">
        <Input
          label="Threshold"
          type="number"
          value={data.threshold}
          onChange={(e) => onChange({ threshold: parseInt(e.target.value, 10) || 25 })}
          helper="0-255. Higher = less sensitive"
          disabled={!data.enabled}
        />
        <Input
          label="Minimum Area"
          type="number"
          value={data.minimumArea}
          onChange={(e) => onChange({ minimumArea: parseInt(e.target.value, 10) || 500 })}
          helper="Pixels. Ignore smaller motion"
          disabled={!data.enabled}
        />
      </div>

      <div className="grid grid-cols-2 gap-4">
        <Input
          label="Cooldown Period"
          type="number"
          value={data.cooldownPeriod}
          onChange={(e) => onChange({ cooldownPeriod: parseInt(e.target.value, 10) || 5 })}
          helper="Seconds between triggers"
          disabled={!data.enabled}
        />
        <Input
          label="No Motion Delay"
          type="number"
          value={data.noMotionDelay}
          onChange={(e) => onChange({ noMotionDelay: parseInt(e.target.value, 10) || 10 })}
          helper="Seconds to wait before stopping"
          disabled={!data.enabled}
        />
      </div>

      <Input
        label="Consecutive Frames"
        type="number"
        value={data.minConsecutiveFrames}
        onChange={(e) => onChange({ minConsecutiveFrames: parseInt(e.target.value, 10) || 3 })}
        helper="Frames with motion before triggering (reduces false positives)"
        disabled={!data.enabled}
      />
    </div>
  );
}

MotionAdvanced.displayName = 'MotionAdvanced';
