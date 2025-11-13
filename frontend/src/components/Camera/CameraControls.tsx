import { useState } from 'react';
import { useConnectionStore } from '../../stores/connectionStore';

export function CameraControls() {
  const [showDebug, setShowDebug] = useState(false);
  const connectionState = useConnectionStore((state) => state.connectionState);
  const debugLogs = useConnectionStore((state) => state.debugLogs);
  const connect = useConnectionStore((state) => state.connect);
  const disconnect = useConnectionStore((state) => state.disconnect);
  const clearDebugLogs = useConnectionStore((state) => state.clearDebugLogs);

  const handleConnect = async () => {
    try {
      await connect();
    } catch (error) {
      console.error('Failed to connect:', error);
    }
  };

  const isConnected = connectionState === 'connected' || connectionState === 'connecting';

  return (
    <div className="w-full max-w-4xl mx-auto space-y-4">
      {/* Control Buttons */}
      <div className="flex gap-4 justify-center">
        {!isConnected ? (
          <button
            onClick={handleConnect}
            className="px-6 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors"
          >
            Connect to Camera
          </button>
        ) : (
          <button
            onClick={disconnect}
            className="px-6 py-2 bg-red-600 hover:bg-red-700 text-white rounded-lg font-medium transition-colors"
          >
            Disconnect
          </button>
        )}
        <button
          onClick={() => setShowDebug(!showDebug)}
          className="px-6 py-2 bg-gray-700 hover:bg-gray-600 text-white rounded-lg font-medium transition-colors"
        >
          {showDebug ? 'Hide Debug' : 'Show Debug'}
        </button>
      </div>

      {/* Debug Log */}
      {showDebug && (
        <div className="bg-gray-800 rounded-lg p-4 border border-gray-700">
          <div className="flex justify-between items-center mb-2">
            <h3 className="text-lg font-semibold text-gray-300">Debug Log</h3>
            <button
              onClick={clearDebugLogs}
              className="px-3 py-1 text-sm bg-gray-700 hover:bg-gray-600 rounded transition-colors"
            >
              Clear
            </button>
          </div>
          <div className="bg-black rounded p-3 h-64 overflow-y-auto font-mono text-xs text-green-400">
            {debugLogs.length === 0 ? (
              <div className="text-gray-500">No debug logs yet...</div>
            ) : (
              debugLogs.map((log, index) => (
                <div key={index} className="whitespace-pre-wrap break-words">
                  {log}
                </div>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  );
}
