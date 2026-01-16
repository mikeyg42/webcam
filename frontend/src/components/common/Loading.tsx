import { motion } from 'motion/react';
import { Loader2 } from 'lucide-react';
import { fadeIn } from '../../lib/motion';

interface LoadingProps {
  message?: string;
  fullScreen?: boolean;
}

export function Loading({ message = 'Loading...', fullScreen = true }: LoadingProps) {
  const content = (
    <motion.div
      variants={fadeIn}
      initial="hidden"
      animate="visible"
      className="text-center space-y-4"
    >
      <div className="w-16 h-16 mx-auto rounded-full bg-accent/10 flex items-center justify-center">
        <Loader2 className="w-8 h-8 text-accent animate-spin" />
      </div>
      <p className="text-sm text-text-secondary">{message}</p>
    </motion.div>
  );

  if (fullScreen) {
    return (
      <div className="flex items-center justify-center min-h-screen bg-bg-primary">
        {content}
      </div>
    );
  }

  return (
    <div className="flex items-center justify-center py-12">
      {content}
    </div>
  );
}

Loading.displayName = 'Loading';
