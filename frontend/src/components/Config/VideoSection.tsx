import { useEffect, useState } from 'react';
import type { VideoSettings, CameraDevice } from '../../types/api';

interface VideoSectionProps {
  data: VideoSettings;
  onChange: (data: Partial<VideoSettings>) => void;
}

type Resolution = '480p' | '720p' | '1080p' | '1440p' | '4K' | 'custom';

interface ResolutionOption {
  label: string;
  width: number;
  height: number;
}

const RESOLUTIONS: Record<Exclude<Resolution, 'custom'>, ResolutionOption> = {
  '480p': { label: '480p (640x480)', width: 640, height: 480 },
  '720p': { label: '720p (1280x720)', width: 1280, height: 720 },
  '1080p': { label: '1080p (1920x1080)', width: 1920, height: 1080 },
  '1440p': { label: '1440p (2560x1440)', width: 2560, height: 1440 },
  '4K': { label: '4K (3840x2160)', width: 3840, height: 2160 },
};

const safeParseInt = (value: string, fallback: number): number => {
  const parsed = parseInt(value, 10);
  return isNaN(parsed) || value === '' ? fallback : parsed;
};

export function VideoSection({ data, onChange }: VideoSectionProps) {
  const [cameras, setCameras] = useState<CameraDevice[]>([]);
  const [loadingCameras, setLoadingCameras] = useState(false);
  const [selectedResolution, setSelectedResolution] = useState<Resolution>('custom');

  useEffect(() => {
    const fetchCameras = async () => {
      setLoadingCameras(true);
      try {
        const response = await fetch('/api/cameras');
        const result = await response.json();
        if (result.success && result.cameras) {
          setCameras(result.cameras);
        }
      } catch (error) {
        console.error('Failed to load cameras:', error);
      } finally {
        setLoadingCameras(false);
      }
    };

    fetchCameras();
  }, []);

  useEffect(() => {
    const matchingResolution = Object.entries(RESOLUTIONS).find(
      ([_, res]) => res.width === data.width && res.height === data.height
    );
    setSelectedResolution(matchingResolution ? (matchingResolution[0] as Resolution) : 'custom');
  }, [data.width, data.height]);

  const handleResolutionChange = (resolution: Resolution) => {
    setSelectedResolution(resolution);
    if (resolution !== 'custom') {
      const { width, height } = RESOLUTIONS[resolution];
      onChange({ width, height });
    }
  };

  return (
    <div className="bg-gray-800 rounded-lg p-6 border border-gray-700">
      <h2 className="text-xl font-semibold mb-4 text-white">Video Settings</h2>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
        <div className="md:col-span-2">
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Camera Device
          </label>
          {loadingCameras ? (
            <div className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-gray-400">
              Loading cameras...
            </div>
          ) : cameras.length === 0 ? (
            <div className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-gray-400">
              No cameras found
            </div>
          ) : (
            <select
              value={data.deviceId || ''}
              onChange={(e) => onChange({ deviceId: e.target.value })}
              className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            >
              <option value="">Default Camera</option>
              {cameras.map((camera) => (
                <option key={camera.deviceId} value={camera.deviceId}>
                  {camera.label} {camera.isDefault ? '(Default)' : ''}
                </option>
              ))}
            </select>
          )}
        </div>

        <div className="md:col-span-2">
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Resolution
          </label>
          <select
            value={selectedResolution}
            onChange={(e) => handleResolutionChange(e.target.value as Resolution)}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
          >
            {Object.entries(RESOLUTIONS).map(([key, res]) => (
              <option key={key} value={key}>
                {res.label}
              </option>
            ))}
            <option value="custom">Custom</option>
          </select>
        </div>

        {selectedResolution === 'custom' && (
          <>
            <div>
              <label className="block text-sm font-medium text-gray-300 mb-2">
                Width (pixels)
              </label>
              <input
                type="number"
                value={data.width || ''}
                onChange={(e) => onChange({ width: safeParseInt(e.target.value, data.width) })}
                className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                min="320"
                max="3840"
                placeholder="e.g., 1920"
              />
            </div>

            <div>
              <label className="block text-sm font-medium text-gray-300 mb-2">
                Height (pixels)
              </label>
              <input
                type="number"
                value={data.height || ''}
                onChange={(e) => onChange({ height: safeParseInt(e.target.value, data.height) })}
                className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
                min="240"
                max="2160"
                placeholder="e.g., 1080"
              />
            </div>
          </>
        )}
      </div>
    </div>
  );
}
