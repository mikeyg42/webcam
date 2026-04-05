import { create } from 'zustand';
import { apiClient } from '../api/client';
import type { RecordingStatus } from '../types/api';

interface RecordingStoreState {
  isRecording: boolean;
  recordingId: string | null;
  recordingState: string; // idle, continuous, event, continuous+event
  framesReceived: number;
  framesDropped: number;
  segmentsCreated: number;
  isLoading: boolean;
  error: string | null;
  pollingInterval: number | null;

  start: () => Promise<void>;
  stop: () => Promise<void>;
  fetchStatus: () => Promise<void>;
  startPolling: () => void;
  stopPolling: () => void;
}

export const useRecordingStore = create<RecordingStoreState>((set, get) => ({
  isRecording: false,
  recordingId: null,
  recordingState: 'idle',
  framesReceived: 0,
  framesDropped: 0,
  segmentsCreated: 0,
  isLoading: false,
  error: null,
  pollingInterval: null,

  start: async () => {
    set({ isLoading: true, error: null });
    try {
      const result = await apiClient.startRecording();
      set({
        isRecording: true,
        recordingId: result.recording_id,
        recordingState: 'continuous',
        isLoading: false,
      });
      get().startPolling();
    } catch (err: any) {
      set({ isLoading: false, error: err.message || 'Failed to start recording' });
    }
  },

  stop: async () => {
    set({ isLoading: true, error: null });
    try {
      await apiClient.stopRecording();
      set({
        isRecording: false,
        recordingId: null,
        recordingState: 'idle',
        isLoading: false,
      });
      get().stopPolling();
    } catch (err: any) {
      set({ isLoading: false, error: err.message || 'Failed to stop recording' });
    }
  },

  fetchStatus: async () => {
    try {
      const status: RecordingStatus = await apiClient.getRecordingStatus();
      set({
        isRecording: status.state.state !== 'idle',
        recordingState: status.state.state,
        recordingId: status.state.active_recording_ids?.[0] ?? null,
        framesReceived: status.frames_received,
        framesDropped: status.frames_dropped,
        segmentsCreated: status.segments_created,
      });
    } catch {
      // Polling failure is not fatal — silently retry next tick
    }
  },

  startPolling: () => {
    const { pollingInterval } = get();
    if (pollingInterval !== null) return;

    // Poll immediately, then every 2 seconds
    get().fetchStatus();
    const id = window.setInterval(() => get().fetchStatus(), 2000);
    set({ pollingInterval: id });
  },

  stopPolling: () => {
    const { pollingInterval } = get();
    if (pollingInterval !== null) {
      clearInterval(pollingInterval);
      set({ pollingInterval: null });
    }
  },
}));
