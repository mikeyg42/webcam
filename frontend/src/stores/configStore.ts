import { create } from 'zustand';
import { apiClient } from '../api/client';
import type { ConfigResponse } from '../types/api';

interface ConfigState {
  config: ConfigResponse | null;
  isLoading: boolean;
  error: string | null;
  isSaving: boolean;

  // Actions
  loadConfig: () => Promise<void>;
  updateConfig: (config: ConfigResponse) => Promise<void>;
  setConfig: (config: ConfigResponse) => void;
  testNotification: () => Promise<boolean>;
}

export const useConfigStore = create<ConfigState>((set) => ({
  config: null,
  isLoading: false,
  error: null,
  isSaving: false,

  loadConfig: async () => {
    set({ isLoading: true, error: null });
    try {
      const config = await apiClient.getConfig();
      set({ config, isLoading: false });
    } catch (error: any) {
      set({ error: error.message, isLoading: false });
      throw error;
    }
  },

  updateConfig: async (config: ConfigResponse) => {
    set({ isSaving: true, error: null });
    try {
      const response = await apiClient.updateConfig(config);
      set({
        config: response.config ?? config,
        isSaving: false
      });
    } catch (error: any) {
      set({ error: error.message, isSaving: false });
      throw error;
    }
  },

  setConfig: (config: ConfigResponse) => {
    set({ config });
  },

  testNotification: async () => {
    try {
      const response = await apiClient.testNotification();
      return response.success;
    } catch (error: any) {
      set({ error: error.message });
      return false;
    }
  },
}));
