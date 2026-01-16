import { forwardRef, useId } from 'react';
import { ChevronDown } from 'lucide-react';
import { cn } from '../../lib/utils';
import { Label } from './Label';

export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

export interface SelectProps extends Omit<React.SelectHTMLAttributes<HTMLSelectElement>, 'children'> {
  /** Label text displayed above the select */
  label?: string;
  /** Helper text shown below the label */
  helper?: string;
  /** Error message (replaces helper when present) */
  error?: string;
  /** Mark as required */
  required?: boolean;
  /** Mark as optional */
  optional?: boolean;
  /** Options to display */
  options: SelectOption[];
  /** Placeholder text when no value selected */
  placeholder?: string;
}

export const Select = forwardRef<HTMLSelectElement, SelectProps>(
  (
    {
      label,
      helper,
      error,
      required,
      optional,
      options,
      placeholder,
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
          <select
            ref={ref}
            id={id}
            disabled={disabled}
            aria-invalid={hasError}
            aria-describedby={hasError ? `${id}-error` : undefined}
            className={cn(
              // Base styles
              'w-full rounded bg-bg-subtle border text-text-primary',
              'font-mono text-sm',
              'appearance-none cursor-pointer',
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
              'h-10 px-3 pr-10',
              className
            )}
            {...props}
          >
            {placeholder && (
              <option value="" disabled>
                {placeholder}
              </option>
            )}
            {options.map((option) => (
              <option
                key={option.value}
                value={option.value}
                disabled={option.disabled}
              >
                {option.label}
              </option>
            ))}
          </select>

          {/* Custom dropdown arrow */}
          <div className="absolute right-3 top-1/2 -translate-y-1/2 pointer-events-none text-text-tertiary">
            <ChevronDown className="w-4 h-4" />
          </div>
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

Select.displayName = 'Select';
