import { useEffect, useState } from 'react';
import { motion, AnimatePresence } from 'motion/react';
import { Focus, CheckCircle, AlertCircle, Loader2, Play, Sparkles } from 'lucide-react';
import { useCalibrationStore } from '../../stores/calibrationStore';
import { Button } from '../primitives/Button';
import { Card } from '../layout/Card';
import { Progress } from '../feedback/Progress';
import { StatusIndicator } from '../feedback/StatusIndicator';
import { slideUp, fadeIn } from '../../lib/motion';

interface CalibrationWizardProps {
  onCalibrationComplete?: () => void;
}

export function CalibrationWizard({ onCalibrationComplete }: CalibrationWizardProps) {
  const { status, isPolling, error, startCalibration, applyCalibration, fetchStatus, stopPolling } = useCalibrationStore();
  const [applySuccess, setApplySuccess] = useState(false);

  useEffect(() => {
    fetchStatus();
    return () => {
      stopPolling();
    };
  }, [fetchStatus, stopPolling]);

  const handleStart = async () => {
    try {
      await startCalibration();
    } catch (error) {
      console.error('Failed to start calibration:', error);
    }
  };

  const handleApply = async () => {
    try {
      setApplySuccess(false);
      await applyCalibration();
      setApplySuccess(true);
      onCalibrationComplete?.();
    } catch (error) {
      console.error('Failed to apply calibration:', error);
      setApplySuccess(false);
    }
  };

  const getStatusType = (): 'idle' | 'loading' | 'success' | 'error' => {
    switch (status?.state) {
      case 'recording':
      case 'processing':
        return 'loading';
      case 'complete':
        return 'success';
      case 'error':
        return 'error';
      default:
        return 'idle';
    }
  };

  const getStateLabel = () => {
    switch (status?.state) {
      case 'recording':
        return 'Recording calibration video...';
      case 'processing':
        return 'Processing calibration data...';
      case 'complete':
        return 'Calibration complete';
      case 'error':
        return 'Calibration error';
      case 'idle':
      default:
        return status?.calibrated ? 'System calibrated' : 'Not calibrated';
    }
  };

  const isInProgress = status?.state === 'recording' || status?.state === 'processing';
  const canStart = !status || status.state === 'idle' || status.state === 'error';
  const canApply = status?.state === 'complete';

  return (
    <motion.div
      variants={slideUp}
      initial="hidden"
      animate="visible"
      className="space-y-6"
    >
      {/* Header */}
      <div className="text-center space-y-2">
        <motion.div
          variants={fadeIn}
          className="inline-flex items-center justify-center w-16 h-16 rounded-full bg-accent/10 mb-4"
        >
          <Focus className="w-8 h-8 text-accent" />
        </motion.div>
        <h1 className="font-display text-2xl font-semibold text-text-primary">
          Motion Detection Calibration
        </h1>
        <p className="text-sm text-text-secondary max-w-md mx-auto">
          This step is <strong>only required for motion detection</strong>. Teach the system what your empty scene looks like to accurately detect motion and trigger recordings.
        </p>
        <p className="text-xs text-text-tertiary max-w-md mx-auto mt-2">
          If you're using continuous recording only, this step is not needed.
        </p>
      </div>

      {/* Error Alert */}
      <AnimatePresence>
        {error && (
          <motion.div
            variants={fadeIn}
            initial="hidden"
            animate="visible"
            exit="exit"
            className="bg-status-error/10 border border-status-error/30 rounded-lg p-4 flex items-start gap-3"
          >
            <AlertCircle className="w-5 h-5 text-status-error flex-shrink-0 mt-0.5" />
            <p className="text-sm text-status-error">{error}</p>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Success Alert */}
      <AnimatePresence>
        {applySuccess && (
          <motion.div
            variants={fadeIn}
            initial="hidden"
            animate="visible"
            exit="exit"
            className="bg-status-success/10 border border-status-success/30 rounded-lg p-4 flex items-start gap-3"
          >
            <CheckCircle className="w-5 h-5 text-status-success flex-shrink-0 mt-0.5" />
            <div>
              <p className="text-sm font-medium text-status-success">Calibration Applied</p>
              <p className="text-xs text-text-secondary mt-1">
                Motion detection is now using the calibrated baseline.
              </p>
            </div>
          </motion.div>
        )}
      </AnimatePresence>

      {/* Main Card */}
      <Card padding="lg">
        <div className="space-y-6">
          {/* Status Section */}
          <div className="flex items-center justify-between">
            <span className="text-xs uppercase tracking-label text-text-tertiary">
              Current Status
            </span>
            <StatusIndicator
              status={getStatusType()}
              label={getStateLabel()}
            />
          </div>

          {/* Progress Bar */}
          <AnimatePresence>
            {status && status.progress > 0 && status.state !== 'complete' && (
              <motion.div
                variants={fadeIn}
                initial="hidden"
                animate="visible"
                exit="exit"
              >
                <Progress value={status.progress} showLabel />
              </motion.div>
            )}
          </AnimatePresence>

          {/* Status Message */}
          {status?.message && (
            <p className="text-sm text-text-secondary bg-bg-subtle rounded-lg p-3">
              {status.message}
            </p>
          )}

          {/* Action Buttons */}
          <div className="flex gap-3">
            {canStart && (
              <Button onClick={handleStart} className="flex-1">
                <Play className="w-4 h-4 mr-2" />
                Start Calibration
              </Button>
            )}

            {isInProgress && (
              <div className="flex-1 flex items-center justify-center gap-2 py-3 px-4 bg-bg-subtle rounded-lg">
                <Loader2 className="w-4 h-4 text-accent animate-spin" />
                <span className="text-sm text-text-secondary">Calibrating...</span>
              </div>
            )}

            {canApply && (
              <Button onClick={handleApply} className="flex-1">
                <Sparkles className="w-4 h-4 mr-2" />
                Apply Calibration
              </Button>
            )}
          </div>

          {/* Polling Indicator */}
          {isPolling && (
            <div className="flex items-center justify-center gap-2 text-xs text-text-tertiary">
              <span className="w-1.5 h-1.5 bg-accent rounded-full animate-pulse" />
              Monitoring progress
            </div>
          )}
        </div>
      </Card>

      {/* Instructions Card */}
      <Card padding="md">
        <h3 className="font-editorial text-sm font-medium text-accent mb-4 italic">
          How to Calibrate
        </h3>
        <ol className="space-y-3">
          {[
            'Ensure the camera has a clear, stable view of the monitoring area',
            'Make sure there is no motion in the frame (no people, pets, or moving objects)',
            'Click "Start Calibration" to record baseline footage',
            'Keep the scene completely static for the duration (~10 seconds)',
            'Once complete, click "Apply Calibration" to activate motion detection',
          ].map((step, index) => (
            <li key={index} className="flex gap-3 text-sm">
              <span className="flex-shrink-0 w-5 h-5 rounded-full bg-bg-subtle text-text-tertiary text-xs flex items-center justify-center font-mono">
                {index + 1}
              </span>
              <span className="text-text-secondary leading-relaxed">{step}</span>
            </li>
          ))}
        </ol>
      </Card>

      {/* Tips Card */}
      <Card padding="sm" className="bg-accent/5 border-accent/20">
        <div className="flex items-start gap-3">
          <div className="w-8 h-8 rounded-full bg-accent/10 flex items-center justify-center flex-shrink-0">
            <Sparkles className="w-4 h-4 text-accent" />
          </div>
          <div>
            <p className="text-xs font-medium text-accent mb-1">Pro Tip</p>
            <p className="text-xs text-text-secondary leading-relaxed">
              For best results, calibrate during typical lighting conditions. If lighting changes
              dramatically (day to night), you may want to recalibrate.
            </p>
          </div>
        </div>
      </Card>
    </motion.div>
  );
}

CalibrationWizard.displayName = 'CalibrationWizard';
