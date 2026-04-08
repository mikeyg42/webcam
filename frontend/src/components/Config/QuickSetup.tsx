import { useState, useEffect } from 'react';
import { motion } from 'motion/react';
import { Camera, Mic, Play, Network } from 'lucide-react';
import { Button } from '../primitives/Button';
import { Select, type SelectOption } from '../primitives/Select';
import { Input } from '../primitives/Input';
import { Card } from '../layout/Card';
import { slideUp } from '../../lib/motion';
import type { ConfigResponse, CameraDevice, MicrophoneDevice } from '../../types/api';

interface QuickSetupProps {
  config: ConfigResponse;
  onUpdate: (section: keyof ConfigResponse, data: Partial<ConfigResponse[keyof ConfigResponse]>) => void;
  onSave: () => void;
  isSaving: boolean;
  hasAdvancedChanges?: boolean;
}

// Recording mode options
type RecordingMode = 'disabled' | 'motion' | 'manual' | 'continuous';

export function QuickSetup({
  config,
  onUpdate,
  onSave,
  isSaving,
  hasAdvancedChanges = false,
}: QuickSetupProps) {
  const [cameras, setCameras] = useState<CameraDevice[]>([]);
  const [microphones, setMicrophones] = useState<MicrophoneDevice[]>([]);
  const [loadingDevices, setLoadingDevices] = useState(true);

  // Fetch available devices
  useEffect(() => {
    const fetchDevices = async () => {
      setLoadingDevices(true);
      try {
        const [cameraRes, micRes] = await Promise.all([
          fetch('/api/cameras'),
          fetch('/api/microphones'),
        ]);

        const cameraData = await cameraRes.json();
        const micData = await micRes.json();

        if (cameraData.success && cameraData.cameras) {
          setCameras(cameraData.cameras);
        }
        if (micData.success && micData.microphones) {
          setMicrophones(micData.microphones);
        }
      } catch (error) {
        console.error('Failed to load devices:', error);
      } finally {
        setLoadingDevices(false);
      }
    };

    fetchDevices();
  }, []);

  // Determine current recording mode from config flags
  const getRecordingMode = (): RecordingMode => {
    if (config.recording.continuousEnabled) return 'continuous';
    if (config.recording.eventEnabled && config.motion.enabled) return 'motion';
    // Manual mode: recording not auto-enabled, but user can press record button
    if (!config.recording.continuousEnabled && !config.recording.eventEnabled && !config.motion.enabled) {
      // Check if this was explicitly set to manual vs disabled
      // Use a convention: if the save directory is set, assume manual; otherwise disabled
      return config.recording.saveDirectory?.trim() ? 'manual' : 'disabled';
    }
    return 'manual';
  };

  const setRecordingMode = (mode: RecordingMode) => {
    switch (mode) {
      case 'disabled':
        onUpdate('recording', { continuousEnabled: false, eventEnabled: false });
        onUpdate('motion', { enabled: false });
        break;
      case 'motion':
        onUpdate('recording', { continuousEnabled: false, eventEnabled: true });
        onUpdate('motion', { enabled: true });
        break;
      case 'manual':
        onUpdate('recording', { continuousEnabled: false, eventEnabled: false });
        onUpdate('motion', { enabled: false });
        break;
      case 'continuous':
        onUpdate('recording', { continuousEnabled: true, eventEnabled: false });
        onUpdate('motion', { enabled: false });
        break;
    }
  };

  // Camera options
  const cameraOptions: SelectOption[] = cameras.map((cam) => ({
    value: cam.deviceId,
    label: cam.label + (cam.isDefault ? ' (Default)' : ''),
  }));

  // Microphone options
  const micOptions: SelectOption[] = [
    { value: '', label: 'No microphone' },
    ...microphones.map((mic) => ({
      value: mic.deviceId,
      label: mic.label + (mic.isDefault ? ' (Default)' : ''),
    })),
  ];

  // Recording mode options
  const recordingModeOptions: SelectOption[] = [
    { value: 'disabled', label: 'Recording disabled' },
    { value: 'motion', label: 'Motion-triggered' },
    { value: 'manual', label: 'Manual (press record)' },
    { value: 'continuous', label: 'Continuous (entire session)' },
  ];

  return (
    <motion.div
      variants={slideUp}
      initial="hidden"
      animate="visible"
      className="space-y-6"
    >
      {/* Header */}
      <div className="text-center mb-8">
        <h2 className="font-display text-2xl font-semibold text-text-primary mb-2">
          Quick Setup
        </h2>
        <p className="text-sm text-text-secondary">
          Configure the essentials to get your camera running
        </p>
      </div>

      {/* Camera Selection */}
      <Card padding="md">
        <div className="flex items-start gap-4">
          <div className="w-10 h-10 rounded-lg bg-accent/10 flex items-center justify-center shrink-0">
            <Camera className="w-5 h-5 text-accent" />
          </div>
          <div className="flex-1 min-w-0">
            <Select
              label="Camera"
              helper="Select the camera device to capture video from"
              options={cameraOptions}
              value={config.video.deviceId}
              onChange={(e) => onUpdate('video', { deviceId: e.target.value })}
              placeholder={loadingDevices ? 'Loading cameras...' : 'Select camera'}
              disabled={loadingDevices || cameras.length === 0}
            />
          </div>
        </div>
      </Card>

      {/* Microphone Selection */}
      <Card padding="md">
        <div className="flex items-start gap-4">
          <div className="w-10 h-10 rounded-lg bg-accent/10 flex items-center justify-center shrink-0">
            <Mic className="w-5 h-5 text-accent" />
          </div>
          <div className="flex-1 min-w-0">
            <Select
              label="Microphone"
              helper="Audio recording is optional"
              optional
              options={micOptions}
              value={config.audio.enabled ? config.audio.deviceId : ''}
              onChange={(e) => {
                const deviceId = e.target.value;
                if (deviceId) {
                  onUpdate('audio', { enabled: true, deviceId });
                } else {
                  onUpdate('audio', { enabled: false, deviceId: '' });
                }
              }}
              placeholder={loadingDevices ? 'Loading microphones...' : undefined}
              disabled={loadingDevices}
            />
          </div>
        </div>
      </Card>

      {/* Recording Mode */}
      <Card padding="md">
        <div className="flex items-start gap-4">
          <div className="w-10 h-10 rounded-lg bg-accent/10 flex items-center justify-center shrink-0">
            <Play className="w-5 h-5 text-accent" />
          </div>
          <div className="flex-1 min-w-0 space-y-3">
            <Select
              label="Recording Mode"
              helper="Motion-triggered saves storage by only recording when motion is detected"
              options={recordingModeOptions}
              value={getRecordingMode()}
              onChange={(e) => setRecordingMode(e.target.value as RecordingMode)}
            />
            {getRecordingMode() === 'continuous' && (
              <div className="bg-accent/5 border border-accent/20 rounded-lg p-3 flex items-start gap-2">
                <span className="text-accent text-sm">ℹ️</span>
                <p className="text-xs text-text-secondary leading-relaxed">
                  <strong className="text-accent">Continuous recording</strong> starts automatically when the camera connects. Skips calibration.
                </p>
              </div>
            )}
            {getRecordingMode() === 'motion' && (
              <div className="bg-bg-subtle border border-border rounded-lg p-3 flex items-start gap-2">
                <span className="text-text-tertiary text-sm">ℹ️</span>
                <p className="text-xs text-text-secondary leading-relaxed">
                  <strong className="text-text-primary">Motion-triggered</strong> automatically enables motion detection and requires calibration to establish a baseline.
                </p>
              </div>
            )}
            {getRecordingMode() === 'manual' && (
              <div className="bg-bg-subtle border border-border rounded-lg p-3 flex items-start gap-2">
                <span className="text-text-tertiary text-sm">ℹ️</span>
                <p className="text-xs text-text-secondary leading-relaxed">
                  <strong className="text-text-primary">Manual recording</strong> — use the Record button in the Camera tab to start and stop. Skips calibration.
                </p>
              </div>
            )}
          </div>
        </div>
      </Card>

      {/* Tailscale Network */}
      <Card padding="md">
        <div className="flex items-start gap-4">
          <div className="w-10 h-10 rounded-lg bg-accent/10 flex items-center justify-center shrink-0">
            <Network className="w-5 h-5 text-accent" />
          </div>
          <div className="flex-1 min-w-0 space-y-4">
            <div>
              <p className="text-sm font-medium text-text-primary mb-1">Tailscale Network</p>
              <p className="text-xs text-text-secondary mb-3">
                Secure mesh network for remote access
              </p>
            </div>
            <div className="grid grid-cols-2 gap-4">
              <Input
                label="Node Name"
                value={config.tailscale.nodeName}
                onChange={(e) => onUpdate('tailscale', { nodeName: e.target.value })}
                helper="Your Tailscale node name"
              />
              <Input
                label="Hostname"
                value={config.tailscale.hostname}
                onChange={(e) => onUpdate('tailscale', { hostname: e.target.value })}
                helper="Network hostname"
              />
            </div>
          </div>
        </div>
      </Card>

      {/* Save Button */}
      <div className="pt-4">
        <Button
          onClick={onSave}
          loading={isSaving}
          size="lg"
          className="w-full"
        >
          {isSaving ? 'Saving...' : 'Start Camera'}
        </Button>

        {hasAdvancedChanges && (
          <p className="text-xs text-text-tertiary text-center mt-2">
            Advanced settings have been modified
          </p>
        )}
      </div>
    </motion.div>
  );
}

QuickSetup.displayName = 'QuickSetup';
