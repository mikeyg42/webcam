// LiveKit client manager with reconnection logic
// Replaces the old ion-sfu WebSocket manager

import {
  Room,
  RoomEvent,
  Track,
  type RemoteTrackPublication,
  type RemoteParticipant,
} from 'livekit-client';

export type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'reconnecting' | 'failed';

export interface WebSocketConfig {
  maxReconnectAttempts?: number;
  reconnectInterval?: number;
  roomId?: string;
}

export class WebSocketManager {
  private room: Room;
  private connectionState: ConnectionState = 'disconnected';
  private reconnectAttempts: number = 0;
  private maxReconnectAttempts: number;
  private reconnectInterval: number;
  private reconnectTimeout: number | null = null;
  private eventHandlers: Map<string, Set<Function>> = new Map();

  constructor(config: WebSocketConfig = {}) {
    this.maxReconnectAttempts = config.maxReconnectAttempts ?? 5;
    this.reconnectInterval = config.reconnectInterval ?? 3000;

    this.room = new Room({
      adaptiveStream: true,
      dynacast: true,
    });

    this.setupRoomListeners();
  }

  async connect(): Promise<void> {
    if (this.connectionState === 'connected' || this.connectionState === 'connecting') {
      console.log('[LiveKit] Already connected or connecting');
      return;
    }

    this.setConnectionState('connecting');
    this.emit('status', 'Connecting to security camera...');

    try {
      // Fetch token and LiveKit URL from the Go backend
      const response = await fetch('/api/livekit-token');
      if (!response.ok) {
        throw new Error(`Token request failed: ${response.status}`);
      }
      const { token, url } = await response.json();
      this.emit('debug', `LiveKit URL: ${url}`);

      await this.room.connect(url, token);
      // Room event handlers below will fire 'connected' etc.
    } catch (error) {
      console.error('[LiveKit] Connection error:', error);
      this.emit('error', error);
      this.handleConnectionFailure();
    }
  }

  disconnect(): void {
    this.clearReconnectTimeout();
    this.room.disconnect();
    this.setConnectionState('disconnected');
    this.emit('status', 'Disconnected');
  }

  getConnectionState(): ConnectionState {
    return this.connectionState;
  }

  // Event emitter pattern (same interface as old ion-sfu manager)
  on(event: string, handler: Function): void {
    if (!this.eventHandlers.has(event)) {
      this.eventHandlers.set(event, new Set());
    }
    this.eventHandlers.get(event)!.add(handler);
  }

  off(event: string, handler: Function): void {
    if (this.eventHandlers.has(event)) {
      this.eventHandlers.get(event)!.delete(handler);
    }
  }

  private emit(event: string, ...args: any[]): void {
    if (this.eventHandlers.has(event)) {
      this.eventHandlers.get(event)!.forEach((handler) => {
        try {
          handler(...args);
        } catch (error) {
          console.error(`[LiveKit] Error in ${event} handler:`, error);
        }
      });
    }
  }

  private setupRoomListeners(): void {
    this.room.on(RoomEvent.Connected, () => {
      this.setConnectionState('connected');
      this.reconnectAttempts = 0;
      this.emit('debug', 'Connected to LiveKit room');
      this.emit('status', 'Connected to security camera');
      this.emit('connected');
    });

    this.room.on(RoomEvent.Disconnected, () => {
      this.emit('debug', 'Disconnected from LiveKit room');
      if (this.connectionState !== 'disconnected') {
        this.handleConnectionFailure();
      }
    });

    this.room.on(RoomEvent.Reconnecting, () => {
      this.setConnectionState('reconnecting');
      this.emit('status', 'Reconnecting...');
      this.emit('debug', 'LiveKit reconnecting');
    });

    this.room.on(RoomEvent.Reconnected, () => {
      this.setConnectionState('connected');
      this.emit('status', 'Reconnected to security camera');
      this.emit('debug', 'LiveKit reconnected');
    });

    this.room.on(
      RoomEvent.TrackSubscribed,
      (track: Track, _pub: RemoteTrackPublication, _participant: RemoteParticipant) => {
        this.emit('debug', `Subscribed to ${track.kind} track`);

        // Emit the LiveKit Track object so the video component can use track.attach()
        // which registers the element with LiveKit's adaptive stream system
        this.emit('track', track);

        if (track.kind === Track.Kind.Video) {
          this.emit('status', 'Receiving video stream');
        }
      },
    );

    this.room.on(RoomEvent.TrackUnsubscribed, (track: Track) => {
      this.emit('debug', `Unsubscribed from ${track.kind} track`);
    });

    this.room.on(RoomEvent.ConnectionQualityChanged, (quality, participant) => {
      this.emit('debug', `Connection quality: ${quality} (${participant.identity})`);
    });
  }

  private setConnectionState(state: ConnectionState): void {
    if (this.connectionState !== state) {
      this.connectionState = state;
      this.emit('connectionStateChange', state);
    }
  }

  private handleConnectionFailure(): void {
    this.clearReconnectTimeout();

    if (this.reconnectAttempts < this.maxReconnectAttempts) {
      this.reconnectAttempts++;
      this.setConnectionState('reconnecting');
      this.emit('status', `Connection lost. Reconnecting... (${this.reconnectAttempts}/${this.maxReconnectAttempts})`);
      this.emit('debug', `Attempting reconnect ${this.reconnectAttempts}/${this.maxReconnectAttempts}`);

      this.reconnectTimeout = setTimeout(() => {
        this.connect();
      }, this.reconnectInterval);
    } else {
      this.setConnectionState('failed');
      this.emit('status', 'Connection failed. Please refresh the page.');
      this.emit('debug', 'Max reconnection attempts reached');
      this.emit('failed');
    }
  }

  private clearReconnectTimeout(): void {
    if (this.reconnectTimeout) {
      clearTimeout(this.reconnectTimeout);
      this.reconnectTimeout = null;
    }
  }
}

// Export singleton factory (same interface as old manager)
let wsManagerInstance: WebSocketManager | null = null;

export function getWebSocketManager(config?: WebSocketConfig): WebSocketManager {
  if (!wsManagerInstance) {
    wsManagerInstance = new WebSocketManager(config);
  }
  return wsManagerInstance;
}

export function resetWebSocketManager(): void {
  if (wsManagerInstance) {
    wsManagerInstance.disconnect();
    wsManagerInstance = null;
  }
}
