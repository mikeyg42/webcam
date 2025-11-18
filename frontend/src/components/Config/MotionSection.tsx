import type { MotionSettings } from '../../types/api';

interface MotionSectionProps {
  data: MotionSettings;
  onChange: (data: Partial<MotionSettings>) => void;
}

export function MotionSection({ data, onChange }: MotionSectionProps) {
  return (
    <div className="bg-gray-800 rounded-lg p-6 border border-gray-700">
      <h2 className="text-xl font-semibold mb-4 text-white">Motion Detection Settings</h2>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Threshold (0-255)
          </label>
          <input
            type="number"
            value={data.threshold}
            onChange={(e) => onChange({ threshold: parseFloat(e.target.value) })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="0"
            max="255"
            step="0.1"
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
            value={data.minimumArea}
            onChange={(e) => onChange({ minimumArea: parseInt(e.target.value) })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="0"
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
            value={data.cooldownPeriod}
            onChange={(e) => onChange({ cooldownPeriod: parseInt(e.target.value) })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="0"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            No Motion Delay (seconds)
          </label>
          <input
            type="number"
            value={data.noMotionDelay}
            onChange={(e) => onChange({ noMotionDelay: parseInt(e.target.value) })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="0"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Min Consecutive Frames
          </label>
          <input
            type="number"
            value={data.minConsecutiveFrames}
            onChange={(e) => onChange({ minConsecutiveFrames: parseInt(e.target.value) })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="1"
          />
        </div>
      </div>
    </div>
  );
}
