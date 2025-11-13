// WebSocket manager for ion-sfu signaling with reconnection logic
// Note: This assumes ion-sdk-js is loaded from CDN in index.html

export type ConnectionState = 'disconnected' | 'connecting' | 'connected' | 'reconnecting' | 'failed';

export interface WebSocketConfig {
  maxReconnectAttempts?: number;
  reconnectInterval?: number;
  roomId?: string;
}

export class WebSocketManager {
  private signal: any = null;
  private client: any = null;
  private websocketUrl: string;
  private roomId: string;
  private connectionState: ConnectionState = 'disconnected';
  private reconnectAttempts: number = 0;
  private maxReconnectAttempts: number;
  private reconnectInterval: number;
  private reconnectTimeout: number | null = null;
  private eventHandlers: Map<string, Set<Function>> = new Map();

  constructor(config: WebSocketConfig = {}) {
    this.maxReconnectAttempts = config.maxReconnectAttempts ?? 5;
    this.reconnectInterval = config.reconnectInterval ?? 3000;
    this.roomId = config.roomId ?? 'cameraRoom';

    // Construct WebSocket URL
    const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const host = window.location.host;
    this.websocketUrl = `${protocol}//${host}/ws?roomId=${encodeURIComponent(this.roomId)}`;
  }

  async connect(): Promise<void> {
    if (this.connectionState === 'connected' || this.connectionState === 'connecting') {
      console.log('[WebSocket] Already connected or connecting');
      return;
    }

    this.setConnectionState('connecting');
    this.emit('status', 'Connecting to security camera...');

    try {
      // Check if ion-sdk-js is loaded
      if (typeof (window as any).IonSDK === 'undefined' || typeof (window as any).Signal === 'undefined') {
        throw new Error('Ion SDK not loaded. Make sure ion-sdk-js is loaded from CDN.');
      }

      // Load WebRTC configuration
      const webrtcConfig = await this.loadWebRTCConfig();

      // Create Signal and Client instances
      const Signal = (window as any).Signal;
      const IonSDK = (window as any).IonSDK;

      this.signal = new Signal.IonSFUJSONRPCSignal(this.websocketUrl);
      this.client = new IonSDK.Client(this.signal, webrtcConfig);

      this.setupSignalListeners();
      this.setupClientListeners();
    } catch (error) {
      console.error('[WebSocket] Connection error:', error);
      this.emit('error', error);
      this.handleConnectionFailure();
    }
  }

  disconnect(): void {
    this.clearReconnectTimeout();

    if (this.client) {
      this.client.close();
      this.client = null;
    }

    if (this.signal) {
      this.signal.close();
      this.signal = null;
    }

    this.setConnectionState('disconnected');
    this.emit('status', 'Disconnected');
  }

  getConnectionState(): ConnectionState {
    return this.connectionState;
  }

  getClient(): any {
    return this.client;
  }

  // Event emitter pattern
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
          console.error(`[WebSocket] Error in ${event} handler:`, error);
        }
      });
    }
  }

  private async loadWebRTCConfig(): Promise<any> {
    try {
      const response = await fetch('/api/webrtc-config');
      if (!response.ok) {
        throw new Error(`HTTP error! status: ${response.status}`);
      }
      const config = await response.json();
      this.emit('debug', `WebRTC config loaded: ${config.iceServers?.length ?? 0} ICE servers`);
      return config;
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : 'Unknown error';
      this.emit('debug', `Failed to load WebRTC config: ${errorMessage}. Using fallback.`);
      // Fallback configuration
      return {
        codec: 'vp9',
        iceServers: [{ urls: 'stun:stun.l.google.com:19302' }],
        iceTransportPolicy: 'all',
      };
    }
  }

  private setupSignalListeners(): void {
    if (!this.signal) return;

    this.signal.onopen = () => {
      this.setConnectionState('connected');
      this.reconnectAttempts = 0;
      this.emit('debug', 'WebSocket connection established');
      this.emit('status', 'Connected to security camera');
      this.emit('connected');

      // Join the room to receive streams
      this.client.join(this.roomId);
    };

    this.signal.onclose = (event: CloseEvent) => {
      this.emit('debug', `WebSocket closed: ${event.code} - ${event.reason}`);

      if (this.connectionState !== 'disconnected') {
        this.handleConnectionFailure();
      }
    };

    this.signal.onerror = (error: Event) => {
      console.error('[WebSocket] Signal error:', error);
      this.emit('error', error);
      this.handleConnectionFailure();
    };
  }

  private setupClientListeners(): void {
    if (!this.client) return;

    this.client.ontrack = (track: MediaStreamTrack, stream: MediaStream) => {
      this.emit('debug', `Received ${track.kind} track`);
      this.emit('track', track, stream);

      if (track.kind === 'video') {
        this.emit('status', 'Receiving video stream');
      }
    };
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

// Export singleton factory
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
