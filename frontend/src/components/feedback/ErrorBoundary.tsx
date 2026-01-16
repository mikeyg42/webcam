import { Component } from 'react';
import type { ErrorInfo, ReactNode } from 'react';
import { AlertTriangle, RefreshCw } from 'lucide-react';
import { Button } from '../primitives/Button';

interface Props {
  children: ReactNode;
  fallback?: ReactNode;
}

interface State {
  hasError: boolean;
  error: Error | null;
}

export class ErrorBoundary extends Component<Props, State> {
  public state: State = {
    hasError: false,
    error: null,
  };

  public static getDerivedStateFromError(error: Error): State {
    return { hasError: true, error };
  }

  public componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    console.error('Uncaught error:', error, errorInfo);
  }

  public render() {
    if (this.state.hasError) {
      if (this.props.fallback) {
        return this.props.fallback;
      }

      return (
        <div className="min-h-screen flex items-center justify-center bg-bg-primary p-6">
          <div className="max-w-lg w-full bg-bg-elevated rounded-lg p-8 border border-status-error/30">
            <div className="flex items-center gap-3 mb-4">
              <div className="w-10 h-10 rounded-full bg-status-error/15 flex items-center justify-center">
                <AlertTriangle className="w-5 h-5 text-status-error" />
              </div>
              <h1 className="font-display text-xl font-semibold text-text-primary">
                Something went wrong
              </h1>
            </div>

            <p className="text-text-secondary text-sm mb-4">
              The application encountered an unexpected error. Please refresh the page to try again.
            </p>

            {this.state.error && (
              <div className="bg-bg-primary rounded border border-border p-4 mb-6">
                <p className="text-xs font-mono text-status-error whitespace-pre-wrap break-words">
                  {this.state.error.toString()}
                </p>
              </div>
            )}

            <Button
              onClick={() => window.location.reload()}
              icon={<RefreshCw className="w-4 h-4" />}
            >
              Refresh Page
            </Button>
          </div>
        </div>
      );
    }

    return this.props.children;
  }
}
