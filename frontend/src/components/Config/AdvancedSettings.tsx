import { Video, Mic, Zap, HardDrive, Database, Network, Bell } from 'lucide-react';
import { Section } from '../layout/Section';
import { VideoAdvanced } from './sections/VideoAdvanced';
import { AudioAdvanced } from './sections/AudioAdvanced';
import { MotionAdvanced } from './sections/MotionAdvanced';
import { RecordingAdvanced } from './sections/RecordingAdvanced';
import { StorageAdvanced } from './sections/StorageAdvanced';
import { NetworkAdvanced } from './sections/NetworkAdvanced';
import { NotificationsAdvanced } from './sections/NotificationsAdvanced';
import type { ConfigResponse } from '../../types/api';

interface AdvancedSettingsProps {
  config: ConfigResponse;
  onUpdate: (section: keyof ConfigResponse, data: Partial<ConfigResponse[keyof ConfigResponse]>) => void;
}

export function AdvancedSettings({ config, onUpdate }: AdvancedSettingsProps) {
  return (
    <div className="space-y-4 mt-8">
      <div className="text-center mb-4">
        <p className="text-xs uppercase tracking-label text-text-tertiary">
          Advanced Settings
        </p>
      </div>

      <Section
        title="Video"
        description="Framerate, bitrate, encoding"
        icon={<Video className="w-4 h-4" />}
      >
        <VideoAdvanced
          data={config.video}
          onChange={(data) => onUpdate('video', data)}
        />
      </Section>

      <Section
        title="Audio"
        description="Sample rate, channels, codec"
        icon={<Mic className="w-4 h-4" />}
      >
        <AudioAdvanced
          data={config.audio}
          onChange={(data) => onUpdate('audio', data)}
        />
      </Section>

      <Section
        title="Motion Detection"
        description="Sensitivity, timing, zones"
        icon={<Zap className="w-4 h-4" />}
      >
        <MotionAdvanced
          data={config.motion}
          onChange={(data) => onUpdate('motion', data)}
        />
      </Section>

      <Section
        title="Recording"
        description="Segments, buffers, retention"
        icon={<HardDrive className="w-4 h-4" />}
      >
        <RecordingAdvanced
          data={config.recording}
          onChange={(data) => onUpdate('recording', data)}
        />
      </Section>

      <Section
        title="Storage"
        description="MinIO and PostgreSQL"
        icon={<Database className="w-4 h-4" />}
      >
        <StorageAdvanced
          data={config.storage}
          onChange={(data) => onUpdate('storage', data)}
        />
      </Section>

      <Section
        title="Network"
        description="Tailscale and WebRTC"
        icon={<Network className="w-4 h-4" />}
      >
        <NetworkAdvanced
          tailscale={config.tailscale}
          webrtc={config.webrtc}
          onUpdateTailscale={(data) => onUpdate('tailscale', data)}
          onUpdateWebRTC={(data) => onUpdate('webrtc', data)}
        />
      </Section>

      <Section
        title="Notifications"
        description="Email alerts"
        icon={<Bell className="w-4 h-4" />}
      >
        <NotificationsAdvanced
          data={config.email}
          onChange={(data) => onUpdate('email', data)}
        />
      </Section>
    </div>
  );
}

AdvancedSettings.displayName = 'AdvancedSettings';
