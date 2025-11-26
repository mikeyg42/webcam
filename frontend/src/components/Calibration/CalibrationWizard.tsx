import { useEffect, useState } from 'react';
import { useCalibrationStore } from '../../stores/calibrationStore';

interface CalibrationWizardProps {
  onCalibrationComplete?: () => void;
}

export function CalibrationWizard({ onCalibrationComplete }: CalibrationWizardProps) {
  const { status, isPolling, error, startCalibration, applyCalibration, fetchStatus, stopPolling } = useCalibrationStore();
  const [applySuccess, setApplySuccess] = useState(false);

  useEffect(() => {
    // Fetch initial status
    fetchStatus();

    // Cleanup polling on unmount
    return () => {
      stopPolling();
    };
  }, [fetchStatus, stopPolling]);

  const handleStart = async () => {
    try {
      await startCalibration();
    } catch (error) {
      console.error('Failed to start calibration:', error);
    }
  };

  const handleApply = async () => {
    try {
      setApplySuccess(false);
      await applyCalibration();
      setApplySuccess(true);
      if (onCalibrationComplete) {
        onCalibrationComplete();
      }
    } catch (error) {
      console.error('Failed to apply calibration:', error);
      setApplySuccess(false);
    }
  };

  const getStateColor = () => {
    switch (status?.state) {
      case 'recording':
      case 'processing':
        return 'text-yellow-400';
      case 'complete':
        return 'text-green-400';
      case 'error':
        return 'text-red-400';
      default:
        return 'text-gray-400';
    }
  };

  const getStateDisplay = () => {
    switch (status?.state) {
      case 'recording':
        return 'Recording calibration video...';
      case 'processing':
        return 'Processing calibration data...';
      case 'complete':
        return 'Calibration complete!';
      case 'error':
        return 'Calibration error';
      case 'idle':
      default:
        return status?.calibrated ? 'System calibrated' : 'Not calibrated';
    }
  };

  return (
    <div className="max-w-4xl mx-auto p-6">
      <h1 className="text-3xl font-bold mb-6 text-white">Motion Detection Calibration</h1>

      {error && (
        <div className="bg-red-900/50 border border-red-500 text-red-200 px-4 py-3 rounded mb-6">
          {error}
        </div>
      )}

      {applySuccess && (
        <div className="bg-green-900/50 border border-green-500 text-green-200 px-4 py-3 rounded mb-6">
          ✓ Calibration applied successfully! Motion detection is now using the calibrated baseline.
        </div>
      )}

      <div className="bg-gray-800 rounded-lg p-6 border border-gray-700 space-y-6">
        {/* Status Display */}
        <div>
          <h2 className="text-xl font-semibold mb-4 text-white">Calibration Status</h2>
          <div className="space-y-2">
            <div className="flex items-center justify-between">
              <span className="text-gray-300">State:</span>
              <span className={`font-semibold ${getStateColor()}`}>
                {getStateDisplay()}
              </span>
            </div>

            {status && status.progress > 0 && status.state !== 'complete' && (
              <div>
                <div className="flex items-center justify-between mb-2">
                  <span className="text-gray-300">Progress:</span>
                  <span className="text-white font-semibold">{Math.round(status.progress)}%</span>
                </div>
                <div className="w-full bg-gray-700 rounded-full h-2">
                  <div
                    className="bg-blue-600 h-2 rounded-full transition-all duration-300"
                    style={{ width: `${status.progress}%` }}
                  />
                </div>
              </div>
            )}

            {status?.message && (
              <div className="text-sm text-gray-400 mt-2">
                {status.message}
              </div>
            )}

            {status?.error && (
              <div className="text-sm text-red-400 mt-2">
                Error: {status.error}
              </div>
            )}
          </div>
        </div>

        {/* Instructions */}
        <div className="bg-gray-900/50 rounded-lg p-4 border border-gray-600">
          <h3 className="font-semibold mb-2 text-white">How to Calibrate</h3>
          <ol className="list-decimal list-inside space-y-1 text-sm text-gray-300">
            <li>Ensure the camera has a clear, stable view of the monitoring area</li>
            <li>Make sure there is no motion in the frame</li>
            <li>Click "Start Calibration" to record baseline footage</li>
            <li>Keep the scene static for the duration of calibration</li>
            <li>Once complete, click "Apply Calibration" to activate</li>
          </ol>
        </div>

        {/* Controls */}
        <div className="flex gap-4">
          {(!status || status.state === 'idle' || status.state === 'error') && (
            <button
              onClick={handleStart}
              className="px-6 py-3 bg-blue-600 hover:bg-blue-700 text-white rounded-lg font-medium transition-colors"
            >
              Start Calibration
            </button>
          )}

          {status && (status.state === 'recording' || status.state === 'processing') && (
            <div className="px-6 py-3 bg-gray-700 text-gray-300 rounded-lg font-medium">
              Calibration in progress... Please wait
            </div>
          )}

          {status && status.state === 'complete' && (
            <button
              onClick={handleApply}
              className="px-6 py-3 bg-green-600 hover:bg-green-700 text-white rounded-lg font-medium transition-colors"
            >
              Apply Calibration
            </button>
          )}
        </div>

        {/* Polling indicator */}
        {isPolling && (
          <div className="flex items-center text-sm text-gray-400">
            <div className="animate-pulse w-2 h-2 bg-blue-500 rounded-full mr-2" />
            Monitoring calibration progress...
          </div>
        )}
      </div>
    </div>
  );
}
