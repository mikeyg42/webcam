import { create } from 'zustand';
import { getWebSocketManager, type ConnectionState } from '../lib/websocket';

interface ConnectionStoreState {
  connectionState: ConnectionState;
  status: string;
  debugLogs: string[];
  stream: MediaStream | null;
  videoTrack: MediaStreamTrack | null;
  error: string | null;

  // Actions
  connect: () => Promise<void>;
  disconnect: () => void;
  addDebugLog: (message: string) => void;
  clearDebugLogs: () => void;
}

const MAX_DEBUG_LOGS = 100;

export const useConnectionStore = create<ConnectionStoreState>((set, get) => {
  const wsManager = getWebSocketManager();

  // Setup WebSocket event handlers
  wsManager.on('connectionStateChange', (state: ConnectionState) => {
    set({ connectionState: state });
  });

  wsManager.on('status', (status: string) => {
    set({ status });
  });

  wsManager.on('debug', (message: string) => {
    get().addDebugLog(message);
  });

  wsManager.on('track', (track: MediaStreamTrack, stream: MediaStream) => {
    set({ stream, videoTrack: track });
  });

  wsManager.on('error', (error: any) => {
    const errorMessage = error?.message || 'Unknown error';
    set({ error: errorMessage });
    get().addDebugLog(`Error: ${errorMessage}`);
  });

  wsManager.on('connected', () => {
    set({ error: null });
  });

  wsManager.on('failed', () => {
    set({ error: 'Connection failed after multiple attempts' });
  });

  return {
    connectionState: 'disconnected',
    status: 'Not connected',
    debugLogs: [],
    stream: null,
    videoTrack: null,
    error: null,

    connect: async () => {
      set({ error: null });
      try {
        await wsManager.connect();
      } catch (error: any) {
        set({ error: error.message });
        throw error;
      }
    },

    disconnect: () => {
      wsManager.disconnect();
      set({
        stream: null,
        videoTrack: null,
        status: 'Disconnected',
        error: null,
      });
    },

    addDebugLog: (message: string) => {
      set((state) => {
        const timestamp = new Date().toLocaleTimeString();
        const newLog = `[${timestamp}] ${message}`;
        const newLogs = [...state.debugLogs, newLog];

        // Keep only the last MAX_DEBUG_LOGS entries
        if (newLogs.length > MAX_DEBUG_LOGS) {
          newLogs.shift();
        }

        return { debugLogs: newLogs };
      });
    },

    clearDebugLogs: () => {
      set({ debugLogs: [] });
    },
  };
});
