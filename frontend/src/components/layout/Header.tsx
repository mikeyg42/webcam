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

export interface HeaderProps {
  /** App title */
  title?: string;
  /** Available tabs */
  tabs: Tab[];
  /** Currently active tab ID */
  activeTab: string;
  /** Tab selection handler */
  onTabChange: (tabId: string) => void;
}

export function Header({
  title = 'Security Camera',
  tabs,
  activeTab,
  onTabChange,
}: HeaderProps) {
  return (
    <header className="sticky top-0 z-40 bg-bg-primary/95 backdrop-blur-sm border-b border-border hidden md:block">
      <div className="max-w-3xl mx-auto px-4">
        <div className="flex items-center justify-between h-16">
          {/* Logo/Title */}
          <h1 className="font-display text-lg font-semibold text-text-primary">
            {title}
          </h1>

          {/* Tab Navigation */}
          <nav className="flex items-center gap-1" role="tablist">
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
                    'relative px-4 py-2 text-xs font-medium uppercase tracking-label rounded',
                    'transition-colors duration-150',
                    'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent',
                    isActive
                      ? 'text-accent'
                      : isLocked
                        ? 'text-text-tertiary cursor-not-allowed'
                        : 'text-text-secondary hover:text-text-primary hover:bg-bg-hover'
                  )}
                >
                  <span className="flex items-center gap-2">
                    {tab.icon}
                    {tab.label}
                    {isLocked && <Lock className="w-3 h-3" />}
                  </span>

                  {/* Active indicator */}
                  {isActive && (
                    <motion.div
                      layoutId="header-tab-indicator"
                      className="absolute inset-x-2 -bottom-[1px] h-0.5 bg-accent rounded-full"
                      transition={{ type: 'spring', stiffness: 500, damping: 30 }}
                    />
                  )}
                </button>
              );
            })}
          </nav>
        </div>
      </div>
    </header>
  );
}

Header.displayName = 'Header';
