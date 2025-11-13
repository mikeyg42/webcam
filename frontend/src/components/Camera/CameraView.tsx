import { useEffect, useRef } from 'react';
import { useConnectionStore } from '../../stores/connectionStore';

export function CameraView() {
  const videoRef = useRef<HTMLVideoElement>(null);
  const stream = useConnectionStore((state) => state.stream);
  const connectionState = useConnectionStore((state) => state.connectionState);
  const status = useConnectionStore((state) => state.status);

  useEffect(() => {
    if (videoRef.current && stream) {
      videoRef.current.srcObject = stream;
    }
  }, [stream]);

  const getStatusColor = () => {
    switch (connectionState) {
      case 'connected':
        return 'text-green-400';
      case 'connecting':
      case 'reconnecting':
        return 'text-yellow-400';
      case 'failed':
        return 'text-red-400';
      default:
        return 'text-gray-400';
    }
  };

  return (
    <div className="flex flex-col items-center space-y-4">
      <div className="relative w-full max-w-4xl aspect-video bg-gray-800 rounded-lg overflow-hidden shadow-lg">
        <video
          ref={videoRef}
          autoPlay
          playsInline
          muted
          className="w-full h-full object-contain"
        />
        {!stream && (
          <div className="absolute inset-0 flex items-center justify-center">
            <div className="text-center">
              <div className={`text-lg font-semibold mb-2 ${getStatusColor()}`}>
                {status}
              </div>
              {connectionState === 'connecting' && (
                <div className="animate-spin rounded-full h-12 w-12 border-b-2 border-blue-500 mx-auto" />
              )}
            </div>
          </div>
        )}
      </div>
      <div className={`text-sm ${getStatusColor()}`}>
        {status}
      </div>
    </div>
  );
}
