import { cn } from '../../lib/utils';

export type StatusType = 'idle' | 'loading' | 'success' | 'warning' | 'error' | 'info';

export interface StatusIndicatorProps {
  /** Status type */
  status: StatusType;
  /** Label text */
  label?: string;
  /** Show pulsing animation */
  pulse?: boolean;
  /** Size variant */
  size?: 'sm' | 'md';
  /** CSS class */
  className?: string;
}

const statusColors: Record<StatusType, { dot: string; text: string }> = {
  idle: {
    dot: 'bg-text-tertiary',
    text: 'text-text-tertiary',
  },
  loading: {
    dot: 'bg-status-info',
    text: 'text-status-info',
  },
  success: {
    dot: 'bg-status-success',
    text: 'text-status-success',
  },
  warning: {
    dot: 'bg-status-warning',
    text: 'text-status-warning',
  },
  error: {
    dot: 'bg-status-error',
    text: 'text-status-error',
  },
  info: {
    dot: 'bg-status-info',
    text: 'text-status-info',
  },
};

const sizeStyles = {
  sm: {
    dot: 'w-2 h-2',
    text: 'text-xs',
  },
  md: {
    dot: 'w-2.5 h-2.5',
    text: 'text-sm',
  },
};

export function StatusIndicator({
  status,
  label,
  pulse = false,
  size = 'md',
  className,
}: StatusIndicatorProps) {
  const colors = statusColors[status];
  const sizes = sizeStyles[size];
  const shouldPulse = pulse || status === 'loading';

  return (
    <div className={cn('inline-flex items-center gap-2', className)}>
      <span className="relative flex">
        {/* Pulse ring */}
        {shouldPulse && (
          <span
            className={cn(
              'absolute inline-flex h-full w-full rounded-full opacity-75 animate-ping',
              colors.dot
            )}
          />
        )}
        {/* Solid dot */}
        <span
          className={cn('relative inline-flex rounded-full', colors.dot, sizes.dot)}
        />
      </span>

      {label && (
        <span className={cn('font-medium', colors.text, sizes.text)}>
          {label}
        </span>
      )}
    </div>
  );
}

StatusIndicator.displayName = 'StatusIndicator';
