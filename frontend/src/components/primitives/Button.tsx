import { forwardRef } from 'react';
import { motion } from 'motion/react';
import { Loader2 } from 'lucide-react';
import { cn } from '../../lib/utils';

export interface ButtonProps {
  /** Visual style variant */
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
  /** Size preset */
  size?: 'sm' | 'md' | 'lg';
  /** Show loading spinner and disable */
  loading?: boolean;
  /** Optional icon to show before text */
  icon?: React.ReactNode;
  /** Button content */
  children?: React.ReactNode;
  /** CSS class name */
  className?: string;
  /** Disabled state */
  disabled?: boolean;
  /** Button type */
  type?: 'button' | 'submit' | 'reset';
  /** Click handler */
  onClick?: React.MouseEventHandler<HTMLButtonElement>;
  /** Form ID */
  form?: string;
}

const variantStyles = {
  primary: 'bg-accent text-text-inverse hover:bg-accent-muted active:bg-accent-muted',
  secondary: 'bg-bg-subtle text-text-primary border border-border hover:bg-bg-hover active:bg-bg-hover',
  ghost: 'text-text-secondary hover:text-text-primary hover:bg-bg-hover active:bg-bg-hover',
  danger: 'bg-status-error text-white hover:bg-red-600 active:bg-red-600',
};

const sizeStyles = {
  sm: 'h-8 px-3 text-xs gap-1.5',
  md: 'h-10 px-4 text-sm gap-2',
  lg: 'h-12 px-6 text-base gap-2.5',
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(
  (
    {
      variant = 'primary',
      size = 'md',
      loading = false,
      icon,
      className,
      children,
      disabled,
      type = 'button',
      onClick,
      form,
    },
    ref
  ) => {
    const isDisabled = disabled || loading;

    return (
      <motion.button
        ref={ref}
        type={type}
        form={form}
        onClick={onClick}
        whileTap={isDisabled ? undefined : { scale: 0.97 }}
        transition={{ duration: 0.1 }}
        disabled={isDisabled}
        className={cn(
          // Base styles
          'inline-flex items-center justify-center rounded font-medium',
          'uppercase tracking-label',
          'transition-colors duration-150',
          'focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent focus-visible:ring-offset-2 focus-visible:ring-offset-bg-primary',
          'disabled:opacity-50 disabled:cursor-not-allowed',
          // Variant and size
          variantStyles[variant],
          sizeStyles[size],
          className
        )}
      >
        {loading ? (
          <Loader2 className="w-4 h-4 animate-spin" />
        ) : icon ? (
          <span className="shrink-0">{icon}</span>
        ) : null}
        {children}
      </motion.button>
    );
  }
);

Button.displayName = 'Button';
