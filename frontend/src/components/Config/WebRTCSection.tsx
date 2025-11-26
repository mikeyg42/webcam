import type { WebRTCSettings } from '../../types/api';

interface WebRTCSectionProps {
  data: WebRTCSettings;
  onChange: (data: Partial<WebRTCSettings>) => void;
}

export function WebRTCSection({ data, onChange }: WebRTCSectionProps) {
  return (
    <div className="bg-gray-800 rounded-lg p-6 border border-gray-700">
      <h2 className="text-xl font-semibold mb-4 text-white">WebRTC Settings</h2>
      <div className="space-y-4">
        <p className="text-sm text-gray-400">
          Configure TURN server credentials for WebRTC peer connections. These are used for NAT traversal when direct peer-to-peer connections fail.
        </p>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              TURN Username
            </label>
            <input
              type="text"
              value={data.username || ''}
              onChange={(e) => onChange({ username: e.target.value })}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              placeholder="e.g., pion"
            />
            <p className="text-xs text-gray-400 mt-1">
              Username for TURN server authentication
            </p>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              TURN Password
            </label>
            <input
              type="password"
              value={data.password || ''}
              onChange={(e) => onChange({ password: e.target.value })}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
              placeholder="Enter TURN password"
            />
            <p className="text-xs text-gray-400 mt-1">
              {data.password ? 'Password is set (shown encrypted for security)' : 'Required for TURN server authentication'}
            </p>
          </div>
        </div>

        <div className="mt-4">
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Quality Priority Mode
          </label>
          <select
            value={data.qualityPriority || 'maximize_quality'}
            onChange={(e) => onChange({ qualityPriority: e.target.value })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            <option value="maximize_quality">Maximize Quality</option>
            <option value="minimize_latency">Minimize Latency</option>
            <option value="minimize_device_strain">Minimize Device Strain</option>
          </select>
          <p className="text-xs text-gray-400 mt-1">
            Controls adaptive streaming behavior: <strong>Maximize Quality</strong> uses full bandwidth for best video quality,{' '}
            <strong>Minimize Latency</strong> prioritizes low delay over quality,{' '}
            <strong>Minimize Device Strain</strong> reduces CPU/GPU load with lower bitrates.
          </p>
        </div>
      </div>
    </div>
  );
}
