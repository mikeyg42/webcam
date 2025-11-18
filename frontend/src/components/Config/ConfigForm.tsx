import { useEffect, useState } from 'react';
import { useConfigStore } from '../../stores/configStore';
import type { ConfigResponse } from '../../types/api';
import { VideoSection } from './VideoSection';
import { AudioSection } from './AudioSection';
import { MotionSection } from './MotionSection';
import { EmailSection } from './EmailSection';

export function ConfigForm() {
  const { config, isLoading, isSaving, error, loadConfig, updateConfig } = useConfigStore();
  const [formData, setFormData] = useState<ConfigResponse | null>(null);
  const [saveStatus, setSaveStatus] = useState<string>('');

  useEffect(() => {
    loadConfig();
  }, [loadConfig]);

  useEffect(() => {
    if (config) {
      setFormData(config);
    }
  }, [config]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!formData) return;

    try {
      setSaveStatus('');
      await updateConfig(formData);
      setSaveStatus('Configuration saved successfully! Restart the application for all changes to take effect.');
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

      {saveStatus && (
        <div className={`px-4 py-3 rounded mb-6 ${
          saveStatus.startsWith('Error')
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

        <EmailSection
          data={formData.email}
          onChange={(data) => updateFormData('email', data)}
        />

        <div className="flex gap-4 pt-6 border-t border-gray-700">
          <button
            type="submit"
            disabled={isSaving}
            className="px-6 py-3 bg-blue-600 hover:bg-blue-700 disabled:bg-gray-600 text-white rounded-lg font-medium transition-colors"
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
