import type { VideoSettings } from '../../types/api';

interface VideoSectionProps {
  data: VideoSettings;
  onChange: (data: Partial<VideoSettings>) => void;
}

export function VideoSection({ data, onChange }: VideoSectionProps) {
  return (
    <div className="bg-gray-800 rounded-lg p-6 border border-gray-700">
      <h2 className="text-xl font-semibold mb-4 text-white">Video Settings</h2>
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
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
