import { Settings, Focus, Video } from 'lucide-react';
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
  /** Page content */
  children: React.ReactNode;
}

export function AppShell({
  activeTab,
  onTabChange,
  configComplete = false,
  calibrationComplete = false,
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
      lockReason: 'Complete configuration first',
    },
    {
      id: 'camera',
      label: 'Camera',
      icon: <Video className="w-5 h-5" />,
      locked: !calibrationComplete,
      lockReason: 'Complete calibration first',
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
          'max-w-lg mx-auto px-4 py-6',
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
