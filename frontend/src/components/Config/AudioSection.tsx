import { useEffect, useState } from 'react';
import type { AudioSettings, MicrophoneDevice } from '../../types/api';

interface AudioSectionProps {
  data: AudioSettings;
  onChange: (data: Partial<AudioSettings>) => void;
}

const safeParseInt = (value: string, fallback: number): number => {
  const parsed = parseInt(value, 10);
  return isNaN(parsed) || value === '' ? fallback : parsed;
};

export function AudioSection({ data, onChange }: AudioSectionProps) {
  const [microphones, setMicrophones] = useState<MicrophoneDevice[]>([]);
  const [loadingMicrophones, setLoadingMicrophones] = useState(false);

  useEffect(() => {
    const fetchMicrophones = async () => {
      setLoadingMicrophones(true);
      try {
        const response = await fetch('/api/microphones');
        const result = await response.json();
        if (result.success && result.microphones) {
          setMicrophones(result.microphones);
        }
      } catch (error) {
        console.error('Failed to load microphones:', error);
      } finally {
        setLoadingMicrophones(false);
      }
    };

    fetchMicrophones();
  }, []);

  return (
    <div className="bg-gray-800 rounded-lg p-6 border border-gray-700">
      <h2 className="text-xl font-semibold mb-4 text-white">Audio Settings</h2>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="md:col-span-2">
          <label className="flex items-center space-x-3 mb-4">
            <input
              type="checkbox"
              checked={data.enabled}
              onChange={(e) => onChange({ enabled: e.target.checked })}
              className="w-5 h-5 rounded border-gray-600 bg-gray-700 text-blue-600 focus:ring-2 focus:ring-blue-500"
            />
            <span className="text-sm font-medium text-gray-300">Enable Audio</span>
          </label>
        </div>

        <div className="md:col-span-2">
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Microphone Device
          </label>
          {loadingMicrophones ? (
            <div className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-gray-400">
              Loading microphones...
            </div>
          ) : microphones.length === 0 ? (
            <div className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-gray-400">
              No microphones found
            </div>
          ) : (
            <select
              value={data.deviceId || ''}
              onChange={(e) => onChange({ deviceId: e.target.value })}
              disabled={!data.enabled}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              <option value="">Default Microphone</option>
              {microphones.map((microphone) => (
                <option key={microphone.deviceId} value={microphone.deviceId}>
                  {microphone.label} {microphone.isDefault ? '(Default)' : ''}
                </option>
              ))}
            </select>
          )}
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Sample Rate (Hz)
          </label>
          <input
            type="number"
            value={data.sampleRate}
            onChange={(e) => onChange({ sampleRate: safeParseInt(e.target.value, data.sampleRate) })}
            disabled={!data.enabled}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50"
            min="8000"
            max="48000"
            step="1000"
            placeholder="e.g., 48000"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Channels
          </label>
          <select
            value={data.channels}
            onChange={(e) => onChange({ channels: safeParseInt(e.target.value, data.channels) })}
            disabled={!data.enabled}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500 disabled:opacity-50"
          >
            <option value={1}>Mono (1)</option>
            <option value={2}>Stereo (2)</option>
          </select>
        </div>
      </div>
    </div>
  );
}
