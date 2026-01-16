import { forwardRef } from 'react';
import { cn } from '../../lib/utils';

export interface LabelProps extends React.LabelHTMLAttributes<HTMLLabelElement> {
  /** Additional helper text shown below the label */
  helper?: string;
  /** Mark as required with asterisk */
  required?: boolean;
  /** Mark as optional (mutually exclusive with required) */
  optional?: boolean;
}

export const Label = forwardRef<HTMLLabelElement, LabelProps>(
  ({ className, children, helper, required, optional, ...props }, ref) => {
    return (
      <div className="space-y-1">
        <label
          ref={ref}
          className={cn(
            // Michelini-inspired label style
            'block text-xs font-medium uppercase tracking-label text-text-secondary',
            className
          )}
          {...props}
        >
          {children}
          {required && <span className="text-status-error ml-1">*</span>}
          {optional && (
            <span className="text-text-tertiary ml-1 normal-case tracking-normal">
              (optional)
            </span>
          )}
        </label>
        {helper && (
          <p className="text-2xs text-text-tertiary normal-case tracking-normal">
            {helper}
          </p>
        )}
      </div>
    );
  }
);

Label.displayName = 'Label';
