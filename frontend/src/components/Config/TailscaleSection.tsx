import type { TailscaleSettings } from '../../types/api';

interface TailscaleSectionProps {
  data: TailscaleSettings;
  onChange: (data: Partial<TailscaleSettings>) => void;
}

const safeParseInt = (value: string, fallback: number): number => {
  const parsed = parseInt(value, 10);
  return isNaN(parsed) || value === '' ? fallback : parsed;
};

export function TailscaleSection({ data, onChange }: TailscaleSectionProps) {
  return (
    <div className="bg-gray-800 rounded-lg p-6 border border-gray-700">
      <h2 className="text-xl font-semibold mb-4 text-white">Tailscale Settings</h2>
      <div className="space-y-4">
        <div>
          <label className="flex items-center space-x-3">
            <input
              type="checkbox"
              checked={data.enabled}
              onChange={(e) => onChange({ enabled: e.target.checked })}
              className="w-5 h-5 rounded border-gray-600 bg-gray-700 text-blue-600 focus:ring-2 focus:ring-blue-500"
            />
            <span className="text-sm font-medium text-gray-300">Enable Tailscale VPN</span>
          </label>
          <p className="text-xs text-gray-400 mt-1 ml-8">
            Connect to your security camera remotely via Tailscale network
          </p>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              Node Name
            </label>
            <input
              type="text"
              value={data.nodeName || ''}
              onChange={(e) => onChange({ nodeName: e.target.value })}
              disabled={!data.enabled}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
              placeholder="e.g., security-camera-1"
            />
            <p className="text-xs text-gray-400 mt-1">
              Unique name for this device on Tailscale network
            </p>
          </div>

          <div>
            <label className="block text-sm font-medium text-gray-300 mb-2">
              Hostname
            </label>
            <input
              type="text"
              value={data.hostname || ''}
              onChange={(e) => onChange({ hostname: e.target.value })}
              disabled={!data.enabled}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
              placeholder="e.g., camera.tailnet.ts.net"
            />
            <p className="text-xs text-gray-400 mt-1">
              Tailscale hostname for this device
            </p>
          </div>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Auth Key
          </label>
          <input
            type="password"
            value={data.authKey || ''}
            onChange={(e) => onChange({ authKey: e.target.value })}
            disabled={!data.enabled}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
            placeholder="Enter auth key (leave blank to keep existing)"
          />
          <p className="text-xs text-gray-400 mt-1">
            Tailscale authentication key for automatic device registration
          </p>
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Listen Port
          </label>
          <input
            type="number"
            value={data.listenPort || ''}
            onChange={(e) => onChange({ listenPort: safeParseInt(e.target.value, data.listenPort) })}
            disabled={!data.enabled}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
            min="1"
            max="65535"
            placeholder="e.g., 41641"
          />
          <p className="text-xs text-gray-400 mt-1">
            UDP port for Tailscale to listen on
          </p>
        </div>
      </div>
    </div>
  );
}
