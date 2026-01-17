import { Input } from '../../primitives/Input';
import { Select } from '../../primitives/Select';
import type { TailscaleSettings, WebRTCSettings } from '../../../types/api';

interface NetworkAdvancedProps {
  tailscale: TailscaleSettings;
  webrtc: WebRTCSettings;
  onUpdateTailscale: (data: Partial<TailscaleSettings>) => void;
  onUpdateWebRTC: (data: Partial<WebRTCSettings>) => void;
}

export function NetworkAdvanced({
  tailscale,
  webrtc,
  onUpdateTailscale,
  onUpdateWebRTC,
}: NetworkAdvancedProps) {
  const qualityOptions = [
    { value: 'maximize_quality', label: 'Maximize Quality' },
    { value: 'minimize_latency', label: 'Minimize Latency' },
    { value: 'minimize_device_strain', label: 'Minimize Device Strain' },
  ];

  return (
    <div className="space-y-6">
      {/* Tailscale Section */}
      <div>
        <p className="text-xs uppercase tracking-label text-text-secondary mb-3">
          Tailscale VPN (Required)
        </p>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <Input
              label="Listen Port"
              type="number"
              value={tailscale.listenPort}
              onChange={(e) => onUpdateTailscale({ listenPort: parseInt(e.target.value, 10) || 41641 })}
              helper="Default: 41641"
            />
            <Input
              label="Auth Key"
              type="password"
              value={tailscale.authKey || ''}
              onChange={(e) => onUpdateTailscale({ authKey: e.target.value })}
              optional
              helper="Optional pre-auth key"
            />
          </div>
        </div>
      </div>

      {/* WebRTC Section */}
      <div>
        <p className="text-xs uppercase tracking-label text-text-secondary mb-3">
          WebRTC Streaming
        </p>
        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <Input
              label="Username"
              value={webrtc.username}
              onChange={(e) => onUpdateWebRTC({ username: e.target.value })}
              helper="WebRTC authentication username"
            />
            <Input
              label="Password"
              type="password"
              value={webrtc.password || ''}
              onChange={(e) => onUpdateWebRTC({ password: e.target.value })}
              helper="WebRTC authentication password"
            />
          </div>
          <Select
            label="Quality Priority"
            options={qualityOptions}
            value={webrtc.qualityPriority || 'maximize_quality'}
            onChange={(e) => onUpdateWebRTC({ qualityPriority: e.target.value })}
            helper="How to optimize streaming quality vs performance"
          />
        </div>
      </div>
    </div>
  );
}

NetworkAdvanced.displayName = 'NetworkAdvanced';
