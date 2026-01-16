import { motion } from 'motion/react';
import { Lock } from 'lucide-react';
import { cn } from '../../lib/utils';

export interface Tab {
  id: string;
  label: string;
  icon?: React.ReactNode;
  locked?: boolean;
  lockReason?: string;
}

export interface TabBarProps {
  /** Available tabs */
  tabs: Tab[];
  /** Currently active tab ID */
  activeTab: string;
  /** Tab selection handler */
  onTabChange: (tabId: string) => void;
}

export function TabBar({ tabs, activeTab, onTabChange }: TabBarProps) {
  return (
    <nav
      className="fixed bottom-0 left-0 right-0 z-40 bg-bg-elevated border-t border-border md:hidden safe-area-pb"
      role="tablist"
    >
      <div className="flex">
        {tabs.map((tab) => {
          const isActive = activeTab === tab.id;
          const isLocked = tab.locked;

          return (
            <button
              key={tab.id}
              type="button"
              role="tab"
              aria-selected={isActive}
              aria-disabled={isLocked}
              onClick={() => !isLocked && onTabChange(tab.id)}
              disabled={isLocked}
              title={isLocked ? tab.lockReason : undefined}
              className={cn(
                'flex-1 flex flex-col items-center justify-center py-3 relative',
                'transition-colors duration-150',
                'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent',
                isLocked && 'opacity-40 cursor-not-allowed'
              )}
            >
              {/* Active indicator at top */}
              {isActive && (
                <motion.div
                  layoutId="tabbar-indicator"
                  className="absolute top-0 left-1/2 -translate-x-1/2 w-8 h-0.5 bg-accent rounded-full"
                  transition={{ type: 'spring', stiffness: 500, damping: 30 }}
                />
              )}

              {/* Icon */}
              <div
                className={cn(
                  'w-6 h-6 relative',
                  isActive ? 'text-accent' : 'text-text-tertiary'
                )}
              >
                {tab.icon}
                {isLocked && (
                  <Lock className="absolute -top-1 -right-1 w-3 h-3 text-text-tertiary" />
                )}
              </div>

              {/* Label */}
              <span
                className={cn(
                  'text-2xs uppercase tracking-label mt-1',
                  isActive ? 'text-accent' : 'text-text-tertiary'
                )}
              >
                {tab.label}
              </span>
            </button>
          );
        })}
      </div>
    </nav>
  );
}

TabBar.displayName = 'TabBar';
