import { Settings, Focus, Video, Film } from 'lucide-react';
import { Header, type Tab } from './Header';
import { TabBar } from './TabBar';
import { cn } from '../../lib/utils';

export interface AppShellProps {
  /** Currently active tab ID */
  activeTab: string;
  /** Tab selection handler */
  onTabChange: (tabId: string) => void;
  /** Whether config is complete (unlocks calibration) */
  configComplete?: boolean;
  /** Whether calibration is complete (unlocks camera) */
  calibrationComplete?: boolean;
  /** Whether calibration is needed (based on recording mode) */
  calibrationNeeded?: boolean;
  /** Page content */
  children: React.ReactNode;
}

export function AppShell({
  activeTab,
  onTabChange,
  configComplete = false,
  calibrationComplete = false,
  calibrationNeeded = true,
  children,
}: AppShellProps) {
  // Define tabs with lock states
  const tabs: Tab[] = [
    {
      id: 'config',
      label: 'Config',
      icon: <Settings className="w-5 h-5" />,
      locked: false,
    },
    {
      id: 'calibration',
      label: 'Calibrate',
      icon: <Focus className="w-5 h-5" />,
      locked: !configComplete,
      lockReason: calibrationNeeded
        ? 'Complete configuration first'
        : 'Not required for continuous recording',
      hidden: !calibrationNeeded && configComplete,
    },
    {
      id: 'camera',
      label: 'Camera',
      icon: <Video className="w-5 h-5" />,
      locked: calibrationNeeded ? !calibrationComplete : !configComplete,
      lockReason: calibrationNeeded
        ? 'Complete calibration first'
        : 'Complete configuration first',
    },
    {
      id: 'recordings',
      label: 'Recordings',
      icon: <Film className="w-5 h-5" />,
      locked: false,
    },
  ];

  return (
    <div className="min-h-screen bg-bg-primary">
      {/* Desktop Header */}
      <Header
        tabs={tabs}
        activeTab={activeTab}
        onTabChange={onTabChange}
      />

      {/* Main Content */}
      <main
        className={cn(
          'mx-auto px-4 py-6',
          activeTab === 'camera' || activeTab === 'recordings' ? 'max-w-4xl' : 'max-w-lg',
          // Add bottom padding on mobile for TabBar
          'pb-24 md:pb-6'
        )}
      >
        {children}
      </main>

      {/* Mobile TabBar */}
      <TabBar
        tabs={tabs}
        activeTab={activeTab}
        onTabChange={onTabChange}
      />
    </div>
  );
}

AppShell.displayName = 'AppShell';
