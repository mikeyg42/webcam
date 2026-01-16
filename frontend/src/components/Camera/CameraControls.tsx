import { useState } from 'react';
import { motion, AnimatePresence } from 'motion/react';
import { Plug, PlugZap, Bug, Trash2, ChevronDown } from 'lucide-react';
import { useConnectionStore } from '../../stores/connectionStore';
import { Button } from '../primitives/Button';
import { Card } from '../layout/Card';
import { slideUp, fadeIn } from '../../lib/motion';

export function CameraControls() {
  const [showDebug, setShowDebug] = useState(false);
  const connectionState = useConnectionStore((state) => state.connectionState);
  const debugLogs = useConnectionStore((state) => state.debugLogs);
  const connect = useConnectionStore((state) => state.connect);
  const disconnect = useConnectionStore((state) => state.disconnect);
  const clearDebugLogs = useConnectionStore((state) => state.clearDebugLogs);

  const handleConnect = async () => {
    try {
      await connect();
    } catch (error) {
      console.error('Failed to connect:', error);
    }
  };

  const isConnected = connectionState === 'connected';
  const isConnecting = connectionState === 'connecting' || connectionState === 'reconnecting';

  return (
    <motion.div
      variants={slideUp}
      initial="hidden"
      animate="visible"
      className="space-y-4"
    >
      {/* Control Buttons */}
      <div className="flex gap-3 justify-center">
        {!isConnected && !isConnecting ? (
          <Button onClick={handleConnect} className="min-w-[180px]">
            <Plug className="w-4 h-4 mr-2" />
            Connect to Camera
          </Button>
        ) : isConnecting ? (
          <Button disabled loading className="min-w-[180px]">
            Connecting...
          </Button>
        ) : (
          <Button onClick={disconnect} variant="danger" className="min-w-[180px]">
            <PlugZap className="w-4 h-4 mr-2" />
            Disconnect
          </Button>
        )}

        <Button
          variant="secondary"
          onClick={() => setShowDebug(!showDebug)}
          className="gap-2"
        >
          <Bug className="w-4 h-4" />
          Debug
          <ChevronDown
            className={`w-4 h-4 transition-transform duration-200 ${showDebug ? 'rotate-180' : ''}`}
          />
        </Button>
      </div>

      {/* Debug Panel */}
      <AnimatePresence>
        {showDebug && (
          <motion.div
            variants={fadeIn}
            initial="hidden"
            animate="visible"
            exit="exit"
          >
            <Card padding="md">
              <div className="flex justify-between items-center mb-3">
                <h3 className="text-xs uppercase tracking-label text-text-tertiary">
                  Debug Log
                </h3>
                <button
                  onClick={clearDebugLogs}
                  className="flex items-center gap-1.5 text-xs text-text-tertiary hover:text-text-secondary transition-colors"
                >
                  <Trash2 className="w-3 h-3" />
                  Clear
                </button>
              </div>
              <div className="bg-bg-primary rounded-lg p-3 h-48 overflow-y-auto font-mono text-xs border border-border">
                {debugLogs.length === 0 ? (
                  <div className="text-text-tertiary h-full flex items-center justify-center">
                    No debug logs yet...
                  </div>
                ) : (
                  <div className="space-y-1">
                    {debugLogs.map((log, index) => (
                      <div
                        key={index}
                        className="text-accent/80 whitespace-pre-wrap break-words leading-relaxed"
                      >
                        {log}
                      </div>
                    ))}
                  </div>
                )}
              </div>
            </Card>
          </motion.div>
        )}
      </AnimatePresence>
    </motion.div>
  );
}

CameraControls.displayName = 'CameraControls';
