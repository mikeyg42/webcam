import { create } from 'zustand';
import { apiClient } from '../api/client';

export interface BrowseRecording {
  id: string;
  type: string;
  status: string;
  started_at: string;
  ended_at?: { Time: string; Valid: boolean };
  duration_seconds: number;
  segment_count: number;
  total_size_bytes: number;
  resolution?: { String: string; Valid: boolean };
  fps?: { Int32: number; Valid: boolean };
  codec?: { String: string; Valid: boolean };
  metadata?: Record<string, any>;
  tags?: string[];
  segments?: BrowseSegment[];
}

export interface BrowseSegment {
  id: string;
  recording_id: string;
  index: number;
  start_time: string;
  end_time: string;
  storage_key: string;
  size_bytes: number;
  status: string;
  url?: string;
}

interface RecordingsBrowserState {
  recordings: BrowseRecording[];
  selectedRecording: BrowseRecording | null;
  typeFilter: string;
  isLoading: boolean;
  error: string | null;

  fetchRecordings: () => Promise<void>;
  selectRecording: (id: string) => Promise<void>;
  clearSelection: () => void;
  deleteRecording: (id: string) => Promise<void>;
  renameRecording: (id: string, name: string) => Promise<void>;
  setTypeFilter: (type: string) => void;
}

export const useRecordingsBrowserStore = create<RecordingsBrowserState>((set, get) => ({
  recordings: [],
  selectedRecording: null,
  typeFilter: '',
  isLoading: false,
  error: null,

  fetchRecordings: async () => {
    set({ isLoading: true, error: null });
    try {
      const { typeFilter } = get();
      const filters: any = { limit: 100 };
      if (typeFilter) filters.type = typeFilter;
      const recordings = await apiClient.listRecordings(filters);
      set({ recordings: recordings || [], isLoading: false });
    } catch (err: any) {
      set({ isLoading: false, error: err.message || 'Failed to load recordings' });
    }
  },

  selectRecording: async (id: string) => {
    try {
      const rec = await apiClient.getRecording(id);
      set({ selectedRecording: rec });
    } catch (err: any) {
      set({ error: err.message || 'Failed to load recording details' });
    }
  },

  clearSelection: () => set({ selectedRecording: null }),

  deleteRecording: async (id: string) => {
    try {
      await apiClient.deleteRecording(id);
      set((state) => ({
        recordings: state.recordings.filter((r) => r.id !== id),
        selectedRecording: state.selectedRecording?.id === id ? null : state.selectedRecording,
      }));
    } catch (err: any) {
      set({ error: err.message || 'Failed to delete recording' });
    }
  },

  renameRecording: async (id: string, name: string) => {
    try {
      const updated = await apiClient.renameRecording(id, name);
      set((state) => ({
        recordings: state.recordings.map((r) => (r.id === id ? updated : r)),
        selectedRecording: state.selectedRecording?.id === id ? updated : state.selectedRecording,
      }));
    } catch (err: any) {
      set({ error: err.message || 'Failed to rename recording' });
    }
  },

  setTypeFilter: (type: string) => {
    set({ typeFilter: type });
    get().fetchRecordings();
  },
}));
