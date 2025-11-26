import { useEffect, useState, useMemo } from 'react';
import { useConfigStore } from '../../stores/configStore';
import type { ConfigResponse } from '../../types/api';
import { VideoSection } from './VideoSection';
import { AudioSection } from './AudioSection';
import { MotionSection } from './MotionSection';
import { EmailSection } from './EmailSection';
import { RecordingSection } from './RecordingSection';
import { StorageSection } from './StorageSection';
import { TailscaleSection } from './TailscaleSection';
import { WebRTCSection } from './WebRTCSection';

interface ConfigFormProps {
  onConfigComplete?: () => void;
}

export function ConfigForm({ onConfigComplete }: ConfigFormProps) {
  const { config, isLoading, isSaving, error, loadConfig, updateConfig } = useConfigStore();
  const [formData, setFormData] = useState<ConfigResponse | null>(null);
  const [saveStatus, setSaveStatus] = useState<string>('');
  const [validationErrors, setValidationErrors] = useState<string[]>([]);

  useEffect(() => {
    loadConfig();
  }, [loadConfig]);

  useEffect(() => {
    if (config) {
      setFormData(config);
    }
  }, [config]);

  const validateForm = useMemo(() => {
    if (!formData) return { isValid: false, errors: [] };

    const errors: string[] = [];

    if (!formData.video.width || formData.video.width < 320 || formData.video.width > 3840) {
      errors.push('Video width must be between 320 and 3840 pixels');
    }
    if (!formData.video.height || formData.video.height < 240 || formData.video.height > 2160) {
      errors.push('Video height must be between 240 and 2160 pixels');
    }

    if (formData.audio.enabled) {
      if (!formData.audio.sampleRate || formData.audio.sampleRate < 8000 || formData.audio.sampleRate > 48000) {
        errors.push('Audio sample rate must be between 8000 and 48000 Hz');
      }
    }

    if (formData.motion.enabled) {
      if (formData.motion.threshold < 0 || formData.motion.threshold > 255) {
        errors.push('Motion threshold must be between 0 and 255');
      }
      if (!formData.motion.minimumArea || formData.motion.minimumArea < 0) {
        errors.push('Motion minimum area must be a positive number');
      }
      if (formData.motion.cooldownPeriod < 0) {
        errors.push('Motion cooldown period must be a positive number');
      }
      if (formData.motion.noMotionDelay < 0) {
        errors.push('Motion no-motion delay must be a positive number');
      }
      if (!formData.motion.minConsecutiveFrames || formData.motion.minConsecutiveFrames < 1) {
        errors.push('Motion minimum consecutive frames must be at least 1');
      }
    }

    if (formData.email.method !== 'disabled') {
      if (!formData.email.fromEmail || !formData.email.fromEmail.includes('@')) {
        errors.push('Valid "from" email address is required');
      }
      if (!formData.email.toEmail || !formData.email.toEmail.includes('@')) {
        errors.push('Valid "to" email address is required');
      }
    }

    if (formData.recording.continuousEnabled || formData.recording.eventEnabled) {
      if (!formData.recording.saveDirectory || formData.recording.saveDirectory.trim() === '') {
        errors.push('Recording save directory is required');
      }
      if (!formData.recording.segmentDuration || formData.recording.segmentDuration < 1) {
        errors.push('Recording segment duration must be at least 1 minute');
      }
      if (!formData.recording.retentionDays || formData.recording.retentionDays < 1) {
        errors.push('Recording retention days must be at least 1 day');
      }
    }

    if (formData.tailscale.enabled) {
      if (!formData.tailscale.nodeName || formData.tailscale.nodeName.trim() === '') {
        errors.push('Tailscale node name is required when enabled');
      }
      if (!formData.tailscale.hostname || formData.tailscale.hostname.trim() === '') {
        errors.push('Tailscale hostname is required when enabled');
      }
      if (!formData.tailscale.listenPort || formData.tailscale.listenPort < 1 || formData.tailscale.listenPort > 65535) {
        errors.push('Tailscale listen port must be between 1 and 65535');
      }
    }

    return { isValid: errors.length === 0, errors };
  }, [formData]);

  useEffect(() => {
    setValidationErrors(validateForm.errors);
  }, [validateForm]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!formData) return;

    if (!validateForm.isValid) {
      setSaveStatus('Please fix validation errors before saving');
      return;
    }

    try {
      setSaveStatus('');
      // Step 1: Save configuration
      await updateConfig(formData);

      // Step 2: Trigger backend restart
      setSaveStatus('Configuration saved! Please hold while we restart the backend to apply changes...');

      try {
        // Import API client dynamically
        const { apiClient } = await import('../../api/client');
        await apiClient.restartBackend();

        // Step 3: Wait for backend to come back up
        setSaveStatus('Backend restarting... Please wait...');

        // Poll health check until backend is back
        let isBackendUp = false;
        const maxAttempts = 30; // 30 seconds max wait

        for (let attempt = 0; attempt < maxAttempts; attempt++) {
          await new Promise(resolve => setTimeout(resolve, 1000)); // Wait 1 second

          try {
            await apiClient.healthCheck();
            isBackendUp = true;
            break;
          } catch (error) {
            // Backend not up yet, continue waiting
          }
        }

        if (isBackendUp) {
          setSaveStatus('✓ Configuration applied successfully! Backend restarted with new settings.');
          if (onConfigComplete) {
            onConfigComplete();
          }
        } else {
          setSaveStatus('Configuration saved, but backend restart is taking longer than expected. Please check manually.');
        }
      } catch (restartError: any) {
        // Restart failed, but config was saved
        setSaveStatus('Configuration saved, but automatic restart failed. Please restart the backend manually using: ./restart-backend-fast.sh');
      }
    } catch (error: any) {
      setSaveStatus(`Error: ${error.message}`);
    }
  };

  const updateFormData = (section: keyof ConfigResponse, data: any) => {
    if (!formData) return;
    setFormData({
      ...formData,
      [section]: {
        ...formData[section],
        ...data,
      },
    });
  };

  if (isLoading || !formData) {
    return (
      <div className="flex items-center justify-center min-h-screen">
        <div className="text-center">
          <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-500 mx-auto mb-4" />
          <p className="text-gray-400">Loading configuration...</p>
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-4xl mx-auto p-6">
      <h1 className="text-3xl font-bold mb-6 text-white">Security Camera Configuration</h1>

      {error && (
        <div className="bg-red-900/50 border border-red-500 text-red-200 px-4 py-3 rounded mb-6">
          {error}
        </div>
      )}

      {validationErrors.length > 0 && (
        <div className="bg-yellow-900/50 border border-yellow-500 text-yellow-200 px-4 py-3 rounded mb-6">
          <p className="font-semibold mb-2">Please fix the following errors:</p>
          <ul className="list-disc list-inside space-y-1">
            {validationErrors.map((error, index) => (
              <li key={index} className="text-sm">{error}</li>
            ))}
          </ul>
        </div>
      )}

      {saveStatus && (
        <div className={`px-4 py-3 rounded mb-6 ${
          saveStatus.startsWith('Error') || saveStatus.startsWith('Please fix')
            ? 'bg-red-900/50 border border-red-500 text-red-200'
            : 'bg-green-900/50 border border-green-500 text-green-200'
        }`}>
          {saveStatus}
        </div>
      )}

      <form onSubmit={handleSubmit} className="space-y-6">
        <VideoSection
          data={formData.video}
          onChange={(data) => updateFormData('video', data)}
        />

        <AudioSection
          data={formData.audio}
          onChange={(data) => updateFormData('audio', data)}
        />

        <MotionSection
          data={formData.motion}
          onChange={(data) => updateFormData('motion', data)}
        />

        <RecordingSection
          data={formData.recording}
          onChange={(data) => updateFormData('recording', data)}
        />

        <StorageSection
          data={formData.storage}
          onChange={(data) => updateFormData('storage', data)}
        />

        <TailscaleSection
          data={formData.tailscale}
          onChange={(data) => updateFormData('tailscale', data)}
        />

        <WebRTCSection
          data={formData.webrtc}
          onChange={(data) => updateFormData('webrtc', data)}
        />

        <EmailSection
          data={formData.email}
          onChange={(data) => updateFormData('email', data)}
        />

        <div className="flex gap-4 pt-6 border-t border-gray-700">
          <button
            type="submit"
            disabled={isSaving || !validateForm.isValid}
            className="px-6 py-3 bg-blue-600 hover:bg-blue-700 disabled:bg-gray-600 disabled:cursor-not-allowed text-white rounded-lg font-medium transition-colors"
          >
            {isSaving ? 'Saving...' : 'Save Configuration'}
          </button>
          <button
            type="button"
            onClick={() => setFormData(config)}
            className="px-6 py-3 bg-gray-700 hover:bg-gray-600 text-white rounded-lg font-medium transition-colors"
          >
            Reset
          </button>
        </div>
      </form>
    </div>
  );
}
