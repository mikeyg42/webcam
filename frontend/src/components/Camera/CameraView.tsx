import { useEffect, useRef } from 'react';
import { motion } from 'motion/react';
import { Video, VideoOff, Loader2 } from 'lucide-react';
import { Track } from 'livekit-client';
import { useConnectionStore } from '../../stores/connectionStore';
import { useRecordingStore } from '../../stores/recordingStore';
import { StatusIndicator } from '../feedback/StatusIndicator';
import { slideUp } from '../../lib/motion';

export function CameraView() {
  const videoRef = useRef<HTMLVideoElement>(null);
  const livekitTrack = useConnectionStore((state) => state.livekitTrack);
  const connectionState = useConnectionStore((state) => state.connectionState);
  const status = useConnectionStore((state) => state.status);
  const isRecording = useRecordingStore((s) => s.isRecording);

  useEffect(() => {
    if (!videoRef.current || !livekitTrack) return;
    if (livekitTrack.kind !== Track.Kind.Video) return;

    // Use LiveKit's attach() — registers element with adaptive stream monitoring
    livekitTrack.attach(videoRef.current);

    return () => {
      livekitTrack.detach(videoRef.current!);
    };
  }, [livekitTrack]);

  const getStatusType = (): 'idle' | 'loading' | 'success' | 'error' => {
    switch (connectionState) {
      case 'connected':
        return 'success';
      case 'connecting':
      case 'reconnecting':
        return 'loading';
      case 'failed':
        return 'error';
      default:
        return 'idle';
    }
  };

  return (
    <motion.div
      variants={slideUp}
      initial="hidden"
      animate="visible"
      className="space-y-4"
    >
      {/* Video Container */}
      <div className="relative w-full aspect-video bg-bg-elevated rounded-lg overflow-hidden border border-border shadow-lg">
        <video
          ref={videoRef}
          autoPlay
          playsInline
          muted
          className="w-full h-full object-contain"
        />

        {/* Overlay when no stream */}
        {!livekitTrack && (
          <div className="absolute inset-0 flex items-center justify-center bg-bg-elevated/80 backdrop-blur-sm">
            <div className="text-center space-y-4">
              {connectionState === 'connecting' || connectionState === 'reconnecting' ? (
                <>
                  <div className="w-16 h-16 mx-auto rounded-full bg-accent/10 flex items-center justify-center">
                    <Loader2 className="w-8 h-8 text-accent animate-spin" />
                  </div>
                  <p className="text-sm text-text-secondary">{status}</p>
                </>
              ) : connectionState === 'failed' ? (
                <>
                  <div className="w-16 h-16 mx-auto rounded-full bg-status-error/10 flex items-center justify-center">
                    <VideoOff className="w-8 h-8 text-status-error" />
                  </div>
                  <p className="text-sm text-status-error">{status}</p>
                </>
              ) : (
                <>
                  <div className="w-16 h-16 mx-auto rounded-full bg-bg-subtle flex items-center justify-center">
                    <Video className="w-8 h-8 text-text-tertiary" />
                  </div>
                  <p className="text-sm text-text-tertiary">
                    Click "Connect" to start viewing
                  </p>
                </>
              )}
            </div>
          </div>
        )}

        {/* Live indicator when connected */}
        {livekitTrack && connectionState === 'connected' && (
          <div className="absolute top-3 left-3 flex items-center gap-3">
            <div className="flex items-center gap-2 bg-bg-primary/80 backdrop-blur-sm px-3 py-1.5 rounded-full border border-border">
              <span className="w-2 h-2 bg-status-success rounded-full animate-pulse" />
              <span className="text-xs font-mono text-status-success uppercase tracking-wider">
                Live
              </span>
            </div>
            {isRecording && (
              <div className="flex items-center gap-2 bg-status-error/90 backdrop-blur-sm px-3 py-1.5 rounded-full">
                <span className="w-2 h-2 bg-white rounded-full animate-pulse" />
                <span className="text-xs font-mono text-white uppercase tracking-wider">
                  Rec
                </span>
              </div>
            )}
          </div>
        )}
      </div>

      {/* Status Bar */}
      <div className="flex items-center justify-center">
        <StatusIndicator status={getStatusType()} label={status} />
      </div>
    </motion.div>
  );
}

CameraView.displayName = 'CameraView';
