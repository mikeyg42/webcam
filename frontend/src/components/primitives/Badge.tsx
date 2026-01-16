import { cn } from '../../lib/utils';

export interface BadgeProps extends React.HTMLAttributes<HTMLSpanElement> {
  /** Visual style variant */
  variant?: 'default' | 'success' | 'warning' | 'error' | 'info' | 'accent';
  /** Size preset */
  size?: 'sm' | 'md';
  /** Show pulsing dot indicator */
  dot?: boolean;
}

const variants = {
  default: 'bg-bg-subtle text-text-secondary border-border',
  success: 'bg-status-success/15 text-status-success border-status-success/30',
  warning: 'bg-status-warning/15 text-status-warning border-status-warning/30',
  error: 'bg-status-error/15 text-status-error border-status-error/30',
  info: 'bg-status-info/15 text-status-info border-status-info/30',
  accent: 'bg-accent/15 text-accent border-accent/30',
};

const dotColors = {
  default: 'bg-text-tertiary',
  success: 'bg-status-success',
  warning: 'bg-status-warning',
  error: 'bg-status-error',
  info: 'bg-status-info',
  accent: 'bg-accent',
};

const sizes = {
  sm: 'text-2xs px-1.5 py-0.5 gap-1',
  md: 'text-xs px-2 py-1 gap-1.5',
};

export function Badge({
  variant = 'default',
  size = 'md',
  dot = false,
  className,
  children,
  ...props
}: BadgeProps) {
  return (
    <span
      className={cn(
        // Base styles
        'inline-flex items-center rounded border font-medium uppercase tracking-label',
        // Variant and size
        variants[variant],
        sizes[size],
        className
      )}
      {...props}
    >
      {dot && (
        <span className="relative flex h-2 w-2">
          <span
            className={cn(
              'absolute inline-flex h-full w-full rounded-full opacity-75 animate-ping',
              dotColors[variant]
            )}
          />
          <span
            className={cn(
              'relative inline-flex h-2 w-2 rounded-full',
              dotColors[variant]
            )}
          />
        </span>
      )}
      {children}
    </span>
  );
}

Badge.displayName = 'Badge';
