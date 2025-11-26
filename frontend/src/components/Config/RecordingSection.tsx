import type { RecordingSettings } from '../../types/api';

interface RecordingSectionProps {
  data: RecordingSettings;
  onChange: (data: Partial<RecordingSettings>) => void;
}

const safeParseInt = (value: string, fallback: number): number => {
  const parsed = parseInt(value, 10);
  return isNaN(parsed) || value === '' ? fallback : parsed;
};

export function RecordingSection({ data, onChange }: RecordingSectionProps) {
  type RecordingMode = 'none' | 'continuous' | 'event';

  const currentMode: RecordingMode =
    data.continuousEnabled ? 'continuous' :
    data.eventEnabled ? 'event' :
    'none';

  const handleModeChange = (mode: RecordingMode) => {
    onChange({
      continuousEnabled: mode === 'continuous',
      eventEnabled: mode === 'event',
    });
  };

  return (
    <div className="bg-gray-800 rounded-lg p-6 border border-gray-700">
      <h2 className="text-xl font-semibold mb-4 text-white">Recording Settings</h2>
      <div className="space-y-4">
        <div>
          <label className="block text-sm font-medium text-gray-300 mb-3">
            Recording Mode
          </label>
          <div className="space-y-2">
            <label className="flex items-center space-x-3">
              <input
                type="radio"
                name="recordingMode"
                checked={currentMode === 'none'}
                onChange={() => handleModeChange('none')}
                className="w-4 h-4 border-gray-600 bg-gray-700 text-blue-600 focus:ring-2 focus:ring-blue-500"
              />
              <div>
                <span className="text-sm font-medium text-gray-300">No Recording</span>
                <p className="text-xs text-gray-400">
                  Disable all recording (streaming only)
                </p>
              </div>
            </label>

            <label className="flex items-center space-x-3">
              <input
                type="radio"
                name="recordingMode"
                checked={currentMode === 'continuous'}
                onChange={() => handleModeChange('continuous')}
                className="w-4 h-4 border-gray-600 bg-gray-700 text-blue-600 focus:ring-2 focus:ring-blue-500"
              />
              <div>
                <span className="text-sm font-medium text-gray-300">Continuous Recording</span>
                <p className="text-xs text-gray-400">
                  Record video continuously, 24/7
                </p>
              </div>
            </label>

            <label className="flex items-center space-x-3">
              <input
                type="radio"
                name="recordingMode"
                checked={currentMode === 'event'}
                onChange={() => handleModeChange('event')}
                className="w-4 h-4 border-gray-600 bg-gray-700 text-blue-600 focus:ring-2 focus:ring-blue-500"
              />
              <div>
                <span className="text-sm font-medium text-gray-300">Event-Based Recording</span>
                <p className="text-xs text-gray-400">
                  Record only when motion is detected
                </p>
              </div>
            </label>
          </div>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Save Directory
          </label>
          <input
            type="text"
            value={data.saveDirectory || ''}
            onChange={(e) => onChange({ saveDirectory: e.target.value })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            placeholder="e.g., /var/recordings"
          />
          <p className="text-xs text-gray-400 mt-1">
            Directory where video recordings will be saved
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              Segment Duration (minutes)
            </label>
            <input
              type="number"
              value={data.segmentDuration || ''}
              onChange={(e) => onChange({ segmentDuration: safeParseInt(e.target.value, data.segmentDuration) })}
              disabled={!data.continuousEnabled && !data.eventEnabled}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
              min="1"
              max="60"
              placeholder="e.g., 15"
            />
            <p className="text-xs text-gray-400 mt-1">
              Length of each video segment file
            </p>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              Retention Days
            </label>
            <input
              type="number"
              value={data.retentionDays || ''}
              onChange={(e) => onChange({ retentionDays: safeParseInt(e.target.value, data.retentionDays) })}
              disabled={!data.continuousEnabled && !data.eventEnabled}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
              min="1"
              max="365"
              placeholder="e.g., 30"
            />
            <p className="text-xs text-gray-400 mt-1">
              How long to keep recordings before deletion
            </p>
          </div>
        </div>

        {data.eventEnabled && (
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <div>
              <label className="block text-sm font-medium text-gray-300 mb-2">
                Pre-Motion Buffer (seconds)
              </label>
              <input
                type="number"
                value={data.preMotionBuffer || ''}
                onChange={(e) => onChange({ preMotionBuffer: safeParseInt(e.target.value, data.preMotionBuffer) })}
                className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                min="0"
                max="30"
                placeholder="e.g., 5"
              />
              <p className="text-xs text-gray-400 mt-1">
                Record this many seconds before motion starts
              </p>
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-300 mb-2">
                Post-Motion Buffer (seconds)
              </label>
              <input
                type="number"
                value={data.postMotionBuffer || ''}
                onChange={(e) => onChange({ postMotionBuffer: safeParseInt(e.target.value, data.postMotionBuffer) })}
                className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                min="0"
                max="60"
                placeholder="e.g., 10"
              />
              <p className="text-xs text-gray-400 mt-1">
                Continue recording this many seconds after motion stops
              </p>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
