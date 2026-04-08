import { motion } from 'motion/react';
import { Lock, Settings, Focus } from 'lucide-react';
import { Button } from '../primitives/Button';
import { slideUp } from '../../lib/motion';

export type LockReason = 'config-required' | 'calibration-required';

interface LockedTabExplainerProps {
  reason: LockReason;
  onNavigate?: (tab: string) => void;
}

const explanations: Record<LockReason, {
  icon: React.ReactNode;
  title: string;
  description: string;
  action: string;
  targetTab: string;
}> = {
  'config-required': {
    icon: <Settings className="w-8 h-8" />,
    title: 'Configuration Required',
    description: 'Complete the Quick Setup to configure your camera before calibrating. This ensures the correct video settings are applied.',
    action: 'Go to Configuration',
    targetTab: 'config',
  },
  'calibration-required': {
    icon: <Focus className="w-8 h-8" />,
    title: 'Motion Detection Calibration Required',
    description: 'Calibrate motion detection before viewing the live camera feed. This step establishes the baseline for detecting motion in your scene and is required when motion detection or event-based recording is enabled.',
    action: 'Start Calibration',
    targetTab: 'calibration',
  },
};

export function LockedTabExplainer({ reason, onNavigate }: LockedTabExplainerProps) {
  const { icon, title, description, action, targetTab } = explanations[reason];

  return (
    <motion.div
      variants={slideUp}
      initial="hidden"
      animate="visible"
      className="flex flex-col items-center justify-center min-h-[400px] text-center px-6"
    >
      {/* Lock icon with accent background */}
      <div className="relative mb-6">
        <div className="w-20 h-20 rounded-full bg-bg-subtle flex items-center justify-center">
          <div className="text-text-tertiary">{icon}</div>
        </div>
        <div className="absolute -bottom-1 -right-1 w-8 h-8 rounded-full bg-bg-elevated border border-border flex items-center justify-center">
          <Lock className="w-4 h-4 text-accent" />
        </div>
      </div>

      {/* Title */}
      <h2 className="font-display text-xl font-semibold text-text-primary mb-3">
        {title}
      </h2>

      {/* Description */}
      <p className="text-sm text-text-secondary max-w-sm mb-8 leading-relaxed">
        {description}
      </p>

      {/* Action button */}
      {onNavigate && (
        <Button onClick={() => onNavigate(targetTab)}>
          {action}
        </Button>
      )}
    </motion.div>
  );
}

LockedTabExplainer.displayName = 'LockedTabExplainer';
