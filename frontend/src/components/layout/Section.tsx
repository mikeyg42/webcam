import { useState } from 'react';
import { motion, AnimatePresence } from 'motion/react';
import { ChevronDown } from 'lucide-react';
import { cn } from '../../lib/utils';

export interface SectionProps {
  /** Section title (uppercase label style) */
  title: string;
  /** Optional description below title */
  description?: string;
  /** Section content */
  children: React.ReactNode;
  /** Start expanded */
  defaultOpen?: boolean;
  /** CSS class for the container */
  className?: string;
  /** Icon to show next to title */
  icon?: React.ReactNode;
}

export function Section({
  title,
  description,
  children,
  defaultOpen = false,
  className,
  icon,
}: SectionProps) {
  const [isOpen, setIsOpen] = useState(defaultOpen);

  return (
    <div
      className={cn(
        'border border-border rounded-lg overflow-hidden',
        className
      )}
    >
      {/* Header - always visible */}
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className={cn(
          'w-full flex items-center justify-between gap-3 px-4 py-3',
          'bg-bg-elevated hover:bg-bg-hover transition-colors duration-150',
          'text-left focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-accent'
        )}
        aria-expanded={isOpen}
      >
        <div className="flex items-center gap-3 min-w-0">
          {icon && (
            <span className="shrink-0 text-text-tertiary">{icon}</span>
          )}
          <div className="min-w-0">
            <span className="block text-xs font-medium uppercase tracking-label text-text-secondary">
              {title}
            </span>
            {description && (
              <span className="block text-2xs text-text-tertiary mt-0.5 truncate">
                {description}
              </span>
            )}
          </div>
        </div>

        <motion.div
          animate={{ rotate: isOpen ? 180 : 0 }}
          transition={{ duration: 0.2 }}
          className="shrink-0"
        >
          <ChevronDown className="w-5 h-5 text-text-tertiary" />
        </motion.div>
      </button>

      {/* Collapsible content */}
      <AnimatePresence initial={false}>
        {isOpen && (
          <motion.div
            initial={{ height: 0, opacity: 0 }}
            animate={{ height: 'auto', opacity: 1 }}
            exit={{ height: 0, opacity: 0 }}
            transition={{ duration: 0.25, ease: [0.25, 0.1, 0.25, 1] }}
            className="overflow-hidden"
          >
            <div className="p-4 bg-bg-primary border-t border-border">
              {children}
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

Section.displayName = 'Section';
