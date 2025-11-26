import type { MotionSettings } from '../../types/api';

interface MotionSectionProps {
  data: MotionSettings;
  onChange: (data: Partial<MotionSettings>) => void;
}

const safeParseInt = (value: string, fallback: number): number => {
  const parsed = parseInt(value, 10);
  return isNaN(parsed) || value === '' ? fallback : parsed;
};

const safeParseFloat = (value: string, fallback: number): number => {
  const parsed = parseFloat(value);
  return isNaN(parsed) || value === '' ? fallback : parsed;
};

export function MotionSection({ data, onChange }: MotionSectionProps) {
  return (
    <div className="bg-gray-800 rounded-lg p-6 border border-gray-700">
      <h2 className="text-xl font-semibold mb-4 text-white">Motion Detection Settings</h2>
      <div className="space-y-4">
        <div>
          <label className="flex items-center space-x-3">
            <input
              type="checkbox"
              checked={data.enabled}
              onChange={(e) => onChange({ enabled: e.target.checked })}
              className="w-5 h-5 rounded border-gray-600 bg-gray-700 text-blue-600 focus:ring-2 focus:ring-blue-500"
            />
            <span className="text-sm font-medium text-gray-300">Enable Motion Detection</span>
          </label>
          <p className="text-xs text-gray-400 mt-1 ml-8">
            When disabled, the camera will stream continuously without motion-triggered recording
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              Threshold (0-255)
            </label>
            <input
              type="number"
              value={data.threshold || ''}
              onChange={(e) => onChange({ threshold: safeParseFloat(e.target.value, data.threshold) })}
              disabled={!data.enabled}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
              min="0"
              max="255"
              step="0.1"
              placeholder="e.g., 25"
            />
            <p className="text-xs text-gray-400 mt-1">
              Higher = less sensitive
            </p>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              Minimum Area (pixels)
            </label>
            <input
              type="number"
              value={data.minimumArea || ''}
              onChange={(e) => onChange({ minimumArea: safeParseInt(e.target.value, data.minimumArea) })}
              disabled={!data.enabled}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
              min="0"
              placeholder="e.g., 500"
            />
            <p className="text-xs text-gray-400 mt-1">
              Minimum pixels to trigger motion
            </p>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              Cooldown Period (seconds)
            </label>
            <input
              type="number"
              value={data.cooldownPeriod || ''}
              onChange={(e) => onChange({ cooldownPeriod: safeParseInt(e.target.value, data.cooldownPeriod) })}
              disabled={!data.enabled}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
              min="0"
              placeholder="e.g., 10"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              No Motion Delay (seconds)
            </label>
            <input
              type="number"
              value={data.noMotionDelay || ''}
              onChange={(e) => onChange({ noMotionDelay: safeParseInt(e.target.value, data.noMotionDelay) })}
              disabled={!data.enabled}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
              min="0"
              placeholder="e.g., 5"
            />
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              Min Consecutive Frames
            </label>
            <input
              type="number"
              value={data.minConsecutiveFrames || ''}
              onChange={(e) => onChange({ minConsecutiveFrames: safeParseInt(e.target.value, data.minConsecutiveFrames) })}
              disabled={!data.enabled}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
              min="1"
              placeholder="e.g., 3"
            />
          </div>
        </div>
      </div>
    </div>
  );
}
