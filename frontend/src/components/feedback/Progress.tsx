import { motion } from 'motion/react';
import { cn } from '../../lib/utils';

export interface ProgressProps {
  /** Current value (0-100) */
  value: number;
  /** Maximum value (default 100) */
  max?: number;
  /** Show percentage label */
  showLabel?: boolean;
  /** Size variant */
  size?: 'sm' | 'md';
  /** Visual variant */
  variant?: 'default' | 'success' | 'warning' | 'error';
  /** CSS class for container */
  className?: string;
}

const sizeStyles = {
  sm: 'h-1',
  md: 'h-2',
};

const barColors = {
  default: 'bg-accent',
  success: 'bg-status-success',
  warning: 'bg-status-warning',
  error: 'bg-status-error',
};

export function Progress({
  value,
  max = 100,
  showLabel = false,
  size = 'md',
  variant = 'default',
  className,
}: ProgressProps) {
  const percentage = Math.min(100, Math.max(0, (value / max) * 100));

  return (
    <div className={cn('w-full', className)}>
      {showLabel && (
        <div className="flex justify-between items-center mb-1">
          <span className="text-xs text-text-secondary">Progress</span>
          <span className="text-xs font-medium text-text-primary">
            {Math.round(percentage)}%
          </span>
        </div>
      )}

      <div
        className={cn(
          'w-full bg-bg-subtle rounded-full overflow-hidden',
          sizeStyles[size]
        )}
        role="progressbar"
        aria-valuenow={value}
        aria-valuemin={0}
        aria-valuemax={max}
      >
        <motion.div
          className={cn('h-full rounded-full', barColors[variant])}
          initial={{ width: 0 }}
          animate={{ width: `${percentage}%` }}
          transition={{ duration: 0.3, ease: [0.25, 0.1, 0.25, 1] }}
        />
      </div>
    </div>
  );
}

Progress.displayName = 'Progress';
