import { useEffect, useState, useMemo } from 'react';
import { motion, AnimatePresence } from 'motion/react';
import { useConfigStore } from '../../stores/configStore';
import { QuickSetup } from './QuickSetup';
import { AdvancedSettings } from './AdvancedSettings';
import { SkeletonCard } from '../feedback/Skeleton';
import { Progress } from '../feedback/Progress';
import { StatusIndicator } from '../feedback/StatusIndicator';
import { Button } from '../primitives/Button';
import { Card } from '../layout/Card';
import { slideUp, fadeIn } from '../../lib/motion';
import type { ConfigResponse } from '../../types/api';

interface ConfigFormProps {
  onConfigComplete?: () => void;
}

type SaveState = 'idle' | 'saving' | 'success' | 'error';

export function ConfigForm({ onConfigComplete }: ConfigFormProps) {
  const { config, isLoading, isSaving, error, loadConfig, updateConfig } = useConfigStore();
  const [formData, setFormData] = useState<ConfigResponse | null>(null);
  const [saveState, setSaveState] = useState<SaveState>('idle');
  const [saveProgress, setSaveProgress] = useState(0);
  const [saveMessage, setSaveMessage] = useState('');
  const [validationErrors, setValidationErrors] = useState<string[]>([]);
  const [showAdvanced, setShowAdvanced] = useState(false);

  useEffect(() => {
    loadConfig();
  }, [loadConfig]);

  useEffect(() => {
    if (config) {
      // Ensure Tailscale is always enabled (it's required for this app)
      setFormData({
        ...config,
        tailscale: { ...config.tailscale, enabled: true },
      });
    }
  }, [config]);

  // Validation logic
  const validateForm = useMemo(() => {
    if (!formData) return { isValid: false, errors: [] };

    const errors: string[] = [];

    // Note: Video resolution is auto-detected from camera, no validation needed

    if (formData.audio.enabled) {
      if (formData.audio.sampleRate < 8000 || formData.audio.sampleRate > 48000) {
        errors.push('Audio sample rate must be between 8000 and 48000 Hz');
      }
    }

    if (formData.motion.enabled) {
      if (formData.motion.threshold < 0 || formData.motion.threshold > 255) {
        errors.push('Motion threshold must be between 0 and 255');
      }
    }

    if (formData.recording.continuousEnabled || formData.recording.eventEnabled) {
      if (!formData.recording.saveDirectory?.trim()) {
        errors.push('Recording save directory is required');
      }
    }

    // Tailscale is always required
    if (!formData.tailscale.nodeName?.trim()) {
      errors.push('Tailscale node name is required');
    }

    return { isValid: errors.length === 0, errors };
  }, [formData]);

  useEffect(() => {
    setValidationErrors(validateForm.errors);
  }, [validateForm]);

  const updateFormData = (section: keyof ConfigResponse, data: Partial<ConfigResponse[keyof ConfigResponse]>) => {
    setFormData((prev) => {
      if (!prev) return prev;
      return {
        ...prev,
        [section]: { ...(prev[section] as object), ...data },
      };
    });
  };

  const handleSave = async () => {
    if (!formData || !validateForm.isValid) return;

    try {
      setSaveState('saving');
      setSaveProgress(50);
      setSaveMessage('Saving configuration...');

      await updateConfig(formData);
      setSaveProgress(100);

      // Config saved - no backend restart needed
      // Motion settings are applied immediately
      // Video/audio settings will be applied when calibration starts the camera
      setSaveState('success');
      setSaveMessage('Configuration saved! Proceed to Calibration to apply camera settings.');
      onConfigComplete?.();
      setTimeout(() => { setSaveState('idle'); setSaveProgress(0); }, 3000);
    } catch (err: unknown) {
      setSaveState('error');
      setSaveMessage(err instanceof Error ? err.message : 'An error occurred');
    }
  };

  if (isLoading || !formData) {
    return <div className="space-y-4"><SkeletonCard /><SkeletonCard /><SkeletonCard /></div>;
  }

  return (
    <div className="space-y-6">
      {error && (
        <motion.div variants={fadeIn} initial="hidden" animate="visible"
          className="bg-status-error/10 border border-status-error/30 rounded-lg p-4">
          <p className="text-sm text-status-error">{error}</p>
        </motion.div>
      )}

      {validationErrors.length > 0 && (
        <motion.div variants={fadeIn} initial="hidden" animate="visible"
          className="bg-status-warning/10 border border-status-warning/30 rounded-lg p-4">
          <p className="text-xs font-medium uppercase tracking-label text-status-warning mb-2">
            Please fix the following:
          </p>
          <ul className="space-y-1">
            {validationErrors.map((err, i) => <li key={i} className="text-sm text-text-secondary">• {err}</li>)}
          </ul>
        </motion.div>
      )}

      <AnimatePresence>
        {saveState !== 'idle' && saveState !== 'success' && (
          <motion.div variants={fadeIn} initial="hidden" animate="visible" exit="exit">
            <Card padding="md">
              <div className="text-center space-y-4">
                <StatusIndicator status={saveState === 'error' ? 'error' : 'loading'} label={saveMessage} />
                {saveState !== 'error' && <Progress value={saveProgress} showLabel />}
                {saveState === 'error' && (
                  <Button variant="secondary" onClick={() => { setSaveState('idle'); setSaveProgress(0); }}>
                    Dismiss
                  </Button>
                )}
              </div>
            </Card>
          </motion.div>
        )}
      </AnimatePresence>

      <AnimatePresence>
        {saveState === 'success' && (
          <motion.div variants={slideUp} initial="hidden" animate="visible" exit="exit"
            className="bg-status-success/10 border border-status-success/30 rounded-lg p-4">
            <StatusIndicator status="success" label={saveMessage} />
          </motion.div>
        )}
      </AnimatePresence>

      {saveState === 'idle' && (
        <QuickSetup config={formData} onUpdate={updateFormData} onSave={handleSave} isSaving={isSaving} />
      )}

      {saveState === 'idle' && (
        <div className="text-center">
          <button type="button" onClick={() => setShowAdvanced(!showAdvanced)}
            className="text-xs uppercase tracking-label text-text-tertiary hover:text-accent transition-colors">
            {showAdvanced ? 'Hide Advanced Settings' : 'Show Advanced Settings'}
          </button>
        </div>
      )}

      <AnimatePresence>
        {showAdvanced && saveState === 'idle' && (
          <motion.div variants={slideUp} initial="hidden" animate="visible" exit="exit">
            <AdvancedSettings config={formData} onUpdate={updateFormData} />
            <div className="mt-6 flex gap-4">
              <Button onClick={handleSave} loading={isSaving} disabled={!validateForm.isValid} className="flex-1">
                Save All Changes
              </Button>
              <Button variant="secondary" onClick={() => setFormData(config)}>Reset</Button>
            </div>
          </motion.div>
        )}
      </AnimatePresence>
    </div>
  );
}

ConfigForm.displayName = 'ConfigForm';
