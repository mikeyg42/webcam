import { useState } from 'react';
import { ErrorBoundary } from './components/common';
import { CameraView, CameraControls } from './components/Camera';
import { ConfigForm } from './components/Config';
import { CalibrationWizard } from './components/Calibration';

type Tab = 'camera' | 'config' | 'calibration';

function App() {
  const [activeTab, setActiveTab] = useState<Tab>('camera');

  const tabs: { id: Tab; label: string }[] = [
    { id: 'camera', label: 'Camera' },
    { id: 'config', label: 'Configuration' },
    { id: 'calibration', label: 'Calibration' },
  ];

  return (
    <ErrorBoundary>
      <div className="min-h-screen bg-gray-900">
        {/* Header */}
        <header className="bg-gray-800 border-b border-gray-700 sticky top-0 z-10">
          <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
            <div className="flex items-center justify-between h-16">
              <div className="flex items-center">
                <h1 className="text-2xl font-bold text-white">
                  Security Camera System
                </h1>
              </div>
              <nav className="flex space-x-4">
                {tabs.map((tab) => (
                  <button
                    key={tab.id}
                    onClick={() => setActiveTab(tab.id)}
                    className={`px-4 py-2 rounded-lg font-medium transition-colors ${
                      activeTab === tab.id
                        ? 'bg-blue-600 text-white'
                        : 'text-gray-300 hover:bg-gray-700 hover:text-white'
                    }`}
                  >
                    {tab.label}
                  </button>
                ))}
              </nav>
            </div>
          </div>
        </header>

        {/* Main Content */}
        <main className="py-8">
          <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
            {activeTab === 'camera' && (
              <div className="space-y-6">
                <CameraView />
                <CameraControls />
              </div>
            )}

            {activeTab === 'config' && <ConfigForm />}

            {activeTab === 'calibration' && <CalibrationWizard />}
          </div>
        </main>

        {/* Footer */}
        <footer className="bg-gray-800 border-t border-gray-700 mt-auto">
          <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-4">
            <p className="text-center text-gray-400 text-sm">
              Security Camera System - Built with React, TypeScript, and WebRTC
            </p>
          </div>
        </footer>
      </div>
    </ErrorBoundary>
  );
}

export default App;
