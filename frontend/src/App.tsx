import { useState, useEffect } from 'react';
import { ErrorBoundary, LockedTabExplainer } from './components/common';
import { CameraView, CameraControls } from './components/Camera';
import { ConfigForm } from './components/Config';
import { CalibrationWizard } from './components/Calibration';
import { RecordingsList } from './components/Recordings';
import { AppShell } from './components/layout';
import { useConfigStore } from './stores/configStore';

type TabId = 'config' | 'calibration' | 'camera' | 'recordings';

function App() {
  const [activeTab, setActiveTab] = useState<TabId>('config');
  const [configCompleted, setConfigCompleted] = useState(false);
  const [calibrationCompleted, setCalibrationCompleted] = useState(false);
  const { config } = useConfigStore();

  const handleTabChange = (tabId: string) => {
    setActiveTab(tabId as TabId);
  };

  // Check if calibration is needed based on recording mode.
  // Calibration is only required for motion detection or event-based recording.
  const calibrationNeeded = config
    ? (config.motion.enabled || config.recording.eventEnabled)
    : true; // Default to true when config hasn't loaded yet

  const handleConfigComplete = () => {
    setConfigCompleted(true);
    // Re-read config from store (just updated by ConfigForm.handleSave)
    const freshConfig = useConfigStore.getState().config;
    const needsCalibration = freshConfig
      ? (freshConfig.motion.enabled || freshConfig.recording.eventEnabled)
      : true;
    if (!needsCalibration) {
      setCalibrationCompleted(true);
      setActiveTab('camera');
    } else {
      setActiveTab('calibration');
    }
  };

  const handleCalibrationComplete = () => {
    setCalibrationCompleted(true);
    setActiveTab('camera');
  };

  // Sync calibration state when config changes:
  // - Auto-complete calibration if not needed (continuous-only mode)
  // - Reset calibration if motion detection is re-enabled (requires new calibration)
  useEffect(() => {
    if (!configCompleted) return;

    if (!calibrationNeeded) {
      // Continuous recording only - skip calibration
      setCalibrationCompleted(true);
    } else {
      // Motion detection enabled - ensure calibration is required.
      // Reset to false so user must complete calibration before accessing camera.
      setCalibrationCompleted(false);
    }
  }, [calibrationNeeded, configCompleted]);

  return (
    <ErrorBoundary>
      <AppShell
        activeTab={activeTab}
        onTabChange={handleTabChange}
        configComplete={configCompleted}
        calibrationComplete={calibrationCompleted}
        calibrationNeeded={calibrationNeeded}
      >
        {/* Configuration Tab */}
        {activeTab === 'config' && (
          <ConfigForm onConfigComplete={handleConfigComplete} />
        )}

        {/* Calibration Tab */}
        {activeTab === 'calibration' && (
          configCompleted ? (
            <CalibrationWizard onCalibrationComplete={handleCalibrationComplete} />
          ) : (
            <LockedTabExplainer
              reason="config-required"
              onNavigate={handleTabChange}
            />
          )
        )}

        {/* Camera Tab */}
        {activeTab === 'camera' && (
          calibrationCompleted ? (
            <div className="space-y-6">
              <CameraView />
              <CameraControls />
            </div>
          ) : (
            <LockedTabExplainer
              reason="calibration-required"
              onNavigate={handleTabChange}
            />
          )
        )}

        {/* Recordings Tab */}
        {activeTab === 'recordings' && (
          <RecordingsList />
        )}
      </AppShell>
    </ErrorBoundary>
  );
}

export default App;
