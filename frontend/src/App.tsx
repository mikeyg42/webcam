import { useState } from 'react';
import { ErrorBoundary, LockedTabExplainer } from './components/common';
import { CameraView, CameraControls } from './components/Camera';
import { ConfigForm } from './components/Config';
import { CalibrationWizard } from './components/Calibration';
import { AppShell } from './components/layout';

type TabId = 'config' | 'calibration' | 'camera';

function App() {
  const [activeTab, setActiveTab] = useState<TabId>('config');
  const [configCompleted, setConfigCompleted] = useState(false);
  const [calibrationCompleted, setCalibrationCompleted] = useState(false);

  const handleTabChange = (tabId: string) => {
    setActiveTab(tabId as TabId);
  };

  const handleConfigComplete = () => {
    setConfigCompleted(true);
    setActiveTab('calibration');
  };

  const handleCalibrationComplete = () => {
    setCalibrationCompleted(true);
    setActiveTab('camera');
  };

  return (
    <ErrorBoundary>
      <AppShell
        activeTab={activeTab}
        onTabChange={handleTabChange}
        configComplete={configCompleted}
        calibrationComplete={calibrationCompleted}
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
      </AppShell>
    </ErrorBoundary>
  );
}

export default App;
