import { useEffect, useState } from 'react';
import type { VideoSettings, CameraDevice } from '../../types/api';

interface VideoSectionProps {
  data: VideoSettings;
  onChange: (data: Partial<VideoSettings>) => void;
}

export function VideoSection({ data, onChange }: VideoSectionProps) {
  const [cameras, setCameras] = useState<CameraDevice[]>([]);
  const [loadingCameras, setLoadingCameras] = useState(false);

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
        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Width (pixels)
          </label>
          <input
            type="number"
            value={data.width}
            onChange={(e) => onChange({ width: parseInt(e.target.value) })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="320"
            max="3840"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Height (pixels)
          </label>
          <input
            type="number"
            value={data.height}
            onChange={(e) => onChange({ height: parseInt(e.target.value) })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="240"
            max="2160"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Framerate (fps)
          </label>
          <input
            type="number"
            value={data.framerate}
            onChange={(e) => onChange({ framerate: parseInt(e.target.value) })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="1"
            max="60"
          />
        </div>

        <div>
          <label className="block text-sm font-medium text-gray-300 mb-2">
            Bitrate (bits/sec)
          </label>
          <input
            type="number"
            value={data.bitRate}
            onChange={(e) => onChange({ bitRate: parseInt(e.target.value) })}
            className="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blue-500"
            min="100000"
            max="10000000"
            step="100000"
          />
        </div>
      </div>
    </div>
  );
}
