import { forwardRef, useId } from 'react';
import { motion } from 'motion/react';
import { cn } from '../../lib/utils';

export interface ToggleProps extends Omit<React.InputHTMLAttributes<HTMLInputElement>, 'type' | 'onChange' | 'size'> {
  /** Current checked state */
  checked?: boolean;
  /** Change handler */
  onChange?: (checked: boolean) => void;
  /** Label text */
  label?: string;
  /** Description text shown below label */
  description?: string;
  /** Size preset */
  size?: 'sm' | 'md';
}

const sizes = {
  sm: {
    track: 'w-8 h-5',
    thumb: 'w-3.5 h-3.5',
    translate: 'translate-x-3.5',
  },
  md: {
    track: 'w-11 h-6',
    thumb: 'w-4.5 h-4.5',
    translate: 'translate-x-5',
  },
};

export const Toggle = forwardRef<HTMLInputElement, ToggleProps>(
  (
    {
      checked = false,
      onChange,
      label,
      description,
      size = 'md',
      className,
      id: providedId,
      disabled,
      ...props
    },
    ref
  ) => {
    const generatedId = useId();
    const id = providedId || generatedId;
    const sizeStyles = sizes[size];

    const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
      onChange?.(e.target.checked);
    };

    return (
      <div className={cn('flex items-start gap-3', className)}>
        <div className="relative shrink-0 mt-0.5">
          <input
            ref={ref}
            type="checkbox"
            id={id}
            checked={checked}
            onChange={handleChange}
            disabled={disabled}
            className="sr-only peer"
            {...props}
          />

          {/* Track */}
          <label
            htmlFor={id}
            className={cn(
              'block rounded-full cursor-pointer transition-colors duration-200',
              'peer-focus-visible:ring-2 peer-focus-visible:ring-accent peer-focus-visible:ring-offset-2 peer-focus-visible:ring-offset-bg-primary',
              'peer-disabled:opacity-50 peer-disabled:cursor-not-allowed',
              checked ? 'bg-accent' : 'bg-bg-subtle border border-border',
              sizeStyles.track
            )}
          >
            {/* Thumb */}
            <motion.span
              layout
              transition={{ type: 'spring', stiffness: 500, damping: 30 }}
              className={cn(
                'block rounded-full shadow-sm',
                checked ? 'bg-text-inverse' : 'bg-text-secondary',
                sizeStyles.thumb,
                // Position
                'absolute top-1/2 -translate-y-1/2 left-0.5',
                checked && sizeStyles.translate
              )}
            />
          </label>
        </div>

        {(label || description) && (
          <div className="flex-1 min-w-0">
            {label && (
              <label
                htmlFor={id}
                className={cn(
                  'block text-sm text-text-primary cursor-pointer',
                  disabled && 'opacity-50 cursor-not-allowed'
                )}
              >
                {label}
              </label>
            )}
            {description && (
              <p className="text-xs text-text-tertiary mt-0.5">{description}</p>
            )}
          </div>
        )}
      </div>
    );
  }
);

Toggle.displayName = 'Toggle';
