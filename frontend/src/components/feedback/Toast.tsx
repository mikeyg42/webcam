import { useEffect } from 'react';
import { motion, AnimatePresence } from 'motion/react';
import { X, CheckCircle, AlertTriangle, XCircle, Info } from 'lucide-react';
import { cn } from '../../lib/utils';

export type ToastType = 'success' | 'warning' | 'error' | 'info';

export interface ToastProps {
  /** Unique identifier */
  id: string;
  /** Toast type determines icon and color */
  type: ToastType;
  /** Title text */
  title: string;
  /** Optional description */
  description?: string;
  /** Auto-dismiss duration in ms (0 to disable) */
  duration?: number;
  /** Dismiss handler */
  onDismiss: (id: string) => void;
}

const toastStyles: Record<ToastType, { icon: React.ReactNode; className: string }> = {
  success: {
    icon: <CheckCircle className="w-5 h-5" />,
    className: 'border-status-success/30 text-status-success',
  },
  warning: {
    icon: <AlertTriangle className="w-5 h-5" />,
    className: 'border-status-warning/30 text-status-warning',
  },
  error: {
    icon: <XCircle className="w-5 h-5" />,
    className: 'border-status-error/30 text-status-error',
  },
  info: {
    icon: <Info className="w-5 h-5" />,
    className: 'border-status-info/30 text-status-info',
  },
};

export function Toast({
  id,
  type,
  title,
  description,
  duration = 5000,
  onDismiss,
}: ToastProps) {
  const { icon, className } = toastStyles[type];

  useEffect(() => {
    if (duration > 0) {
      const timer = setTimeout(() => onDismiss(id), duration);
      return () => clearTimeout(timer);
    }
  }, [id, duration, onDismiss]);

  return (
    <motion.div
      layout
      initial={{ opacity: 0, y: -20, scale: 0.95 }}
      animate={{ opacity: 1, y: 0, scale: 1 }}
      exit={{ opacity: 0, y: -20, scale: 0.95 }}
      transition={{ duration: 0.2, ease: [0.25, 0.1, 0.25, 1] }}
      className={cn(
        'bg-bg-elevated border rounded-lg shadow-lg p-4 min-w-[300px] max-w-[400px]',
        className
      )}
    >
      <div className="flex gap-3">
        <div className="shrink-0">{icon}</div>

        <div className="flex-1 min-w-0">
          <p className="font-medium text-text-primary text-sm">{title}</p>
          {description && (
            <p className="text-text-secondary text-xs mt-1">{description}</p>
          )}
        </div>

        <button
          type="button"
          onClick={() => onDismiss(id)}
          className="shrink-0 text-text-tertiary hover:text-text-primary transition-colors"
        >
          <X className="w-4 h-4" />
        </button>
      </div>
    </motion.div>
  );
}

// Toast container component for managing multiple toasts
export interface ToastContainerProps {
  toasts: Omit<ToastProps, 'onDismiss'>[];
  onDismiss: (id: string) => void;
}

export function ToastContainer({ toasts, onDismiss }: ToastContainerProps) {
  return (
    <div className="fixed top-4 right-4 z-50 flex flex-col gap-2">
      <AnimatePresence mode="popLayout">
        {toasts.map((toast) => (
          <Toast key={toast.id} {...toast} onDismiss={onDismiss} />
        ))}
      </AnimatePresence>
    </div>
  );
}

Toast.displayName = 'Toast';
ToastContainer.displayName = 'ToastContainer';
