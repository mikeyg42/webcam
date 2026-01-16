import { forwardRef, useId } from 'react';
import { cn } from '../../lib/utils';
import { Label } from './Label';

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  /** Label text displayed above the input */
  label?: string;
  /** Helper text shown below the label */
  helper?: string;
  /** Error message (replaces helper when present) */
  error?: string;
  /** Mark as required */
  required?: boolean;
  /** Mark as optional */
  optional?: boolean;
  /** Left icon/addon */
  leftIcon?: React.ReactNode;
  /** Right icon/addon */
  rightIcon?: React.ReactNode;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(
  (
    {
      label,
      helper,
      error,
      required,
      optional,
      leftIcon,
      rightIcon,
      className,
      id: providedId,
      disabled,
      ...props
    },
    ref
  ) => {
    const generatedId = useId();
    const id = providedId || generatedId;
    const hasError = Boolean(error);

    return (
      <div className="space-y-2">
        {label && (
          <Label
            htmlFor={id}
            helper={!error ? helper : undefined}
            required={required}
            optional={optional}
          >
            {label}
          </Label>
        )}

        <div className="relative">
          {leftIcon && (
            <div className="absolute left-3 top-1/2 -translate-y-1/2 text-text-tertiary">
              {leftIcon}
            </div>
          )}

          <input
            ref={ref}
            id={id}
            disabled={disabled}
            aria-invalid={hasError}
            aria-describedby={hasError ? `${id}-error` : undefined}
            className={cn(
              // Base styles
              'w-full rounded bg-bg-subtle border text-text-primary',
              'font-mono text-sm',
              'placeholder:text-text-tertiary',
              'transition-colors duration-150',
              // Focus state
              'focus:outline-none focus:ring-2 focus:ring-accent focus:border-transparent',
              // Disabled state
              'disabled:opacity-50 disabled:cursor-not-allowed',
              // Error state
              hasError
                ? 'border-status-error focus:ring-status-error'
                : 'border-border hover:border-border-accent',
              // Size and padding
              'h-10 px-3',
              leftIcon && 'pl-10',
              rightIcon && 'pr-10',
              className
            )}
            {...props}
          />

          {rightIcon && (
            <div className="absolute right-3 top-1/2 -translate-y-1/2 text-text-tertiary">
              {rightIcon}
            </div>
          )}
        </div>

        {error && (
          <p id={`${id}-error`} className="text-xs text-status-error" role="alert">
            {error}
          </p>
        )}
      </div>
    );
  }
);

Input.displayName = 'Input';
