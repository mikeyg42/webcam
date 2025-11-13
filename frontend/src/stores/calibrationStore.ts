import { create } from 'zustand';
import { apiClient } from '../api/client';
import type { CalibrationStatus } from '../types/api';

interface CalibrationState {
  status: CalibrationStatus | null;
  isPolling: boolean;
  error: string | null;
  pollingInterval: number | null;

  // Actions
  startCalibration: () => Promise<void>;
  applyCalibration: () => Promise<void>;
  fetchStatus: () => Promise<void>;
  startPolling: () => void;
  stopPolling: () => void;
}

export const useCalibrationStore = create<CalibrationState>((set, get) => ({
  status: null,
  isPolling: false,
  error: null,
  pollingInterval: null,

  startCalibration: async () => {
    set({ error: null });
    try {
      await apiClient.startCalibration();
      get().startPolling();
    } catch (error: any) {
      set({ error: error.message });
      throw error;
    }
  },

  applyCalibration: async () => {
    set({ error: null });
    try {
      await apiClient.applyCalibration();
      get().stopPolling();
      await get().fetchStatus();
    } catch (error: any) {
      set({ error: error.message });
      throw error;
    }
  },

  fetchStatus: async () => {
    try {
      const status = await apiClient.getCalibrationStatus();
      set({ status, error: null });

      // Auto-stop polling when calibration is complete or errored
      if (status.state === 'complete' || status.state === 'error') {
        get().stopPolling();
      }
    } catch (error: any) {
      set({ error: error.message });
    }
  },

  startPolling: () => {
    const state = get();

    // Always clear any existing interval first to prevent leaks
    if (state.pollingInterval) {
      clearInterval(state.pollingInterval);
    }

    if (state.isPolling) return;

    // Fetch immediately
    state.fetchStatus();

    // Then poll every 500ms
    const interval = setInterval(() => {
      get().fetchStatus();
    }, 500);

    set({ isPolling: true, pollingInterval: interval });
  },

  stopPolling: () => {
    const { pollingInterval } = get();
    if (pollingInterval) {
      clearInterval(pollingInterval);
      set({ isPolling: false, pollingInterval: null });
    }
  },
}));
