const remotesDiv = document.getElementById("remotes");
const localDiv = document.getElementById("local");
const statusDiv = document.getElementById("status");
const debugDiv = document.getElementById("debug");

// --- Configuration ---

// Get Room ID from URL query parameter (e.g., ?roomId=myRoom)
const urlParams = new URLSearchParams(window.location.search);
const roomId = urlParams.get('roomId') || 'defaultRoom';

// Get server address from current location
const serverProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
const serverHost = window.location.host;
const websocketUrl = `${serverProtocol}//${serverHost}/ws?roomId=${encodeURIComponent(roomId)}`;

// Tailscale integration state
let tailscaleInfo = {
    enabled: false,
    localIP: null,
    hostname: null,
    peers: [],
    networkType: 'traditional'
};

// WebRTC Configuration - Enhanced for Tailscale
let webrtcConfig = {
    codec: 'vp9',
    iceServers: [
        // Default STUN server for initial connectivity
        { urls: "stun:stun.l.google.com:19302" }
    ],
    iceTransportPolicy: 'all', // Allow both direct and relay connections
    iceCandidatePoolSize: 10,   // Faster connection establishment
    bundlePolicy: 'balanced',   // Optimize for mixed network scenarios
    rtcpMuxPolicy: 'require'    // Standard for modern WebRTC
};

document.title = `Webcam Viewer - Room: ${roomId}`;

// --- Tailscale Network Detection and Configuration ---

async function detectAndConfigureTailscale() {
    try {
        logDebug("Checking for Tailscale network configuration...");
        
        // Fetch Tailscale status from server
        const response = await fetch('/tailscale/status');
        if (response.ok) {
            const status = await response.json();
            if (status.connected) {
                tailscaleInfo = {
                    enabled: true,
                    localIP: status.ip,
                    hostname: status.hostname,
                    peers: [],
                    networkType: 'tailscale'
                };
                
                // Fetch peer information
                const peersResponse = await fetch('/tailscale/peers');
                if (peersResponse.ok) {
                    tailscaleInfo.peers = await peersResponse.json();
                }
                
                // Update WebRTC configuration for Tailscale
                configureWebRTCForTailscale();
                
                logDebug(`Tailscale detected - Local IP: ${tailscaleInfo.localIP}, Hostname: ${tailscaleInfo.hostname}`);
                logDebug(`Available peers: ${tailscaleInfo.peers.length}`);
                
                return true;
            }
        }
    } catch (error) {
        logDebug(`Tailscale detection failed: ${error.message}`);
    }
    
    // Fall back to traditional configuration
    configureWebRTCTraditional();
    logDebug("Using traditional WebRTC networking");
    return false;
}

function configureWebRTCForTailscale() {
    // Optimized configuration for Tailscale networks
    webrtcConfig = {
        ...webrtcConfig,
        iceServers: [
            // Keep STUN for initial connectivity discovery
            { urls: "stun:stun.l.google.com:19302" },
            // Additional STUN servers for redundancy
            { urls: "stun:stun1.l.google.com:19302" }
        ],
        // Prefer direct connections since Tailscale handles NAT traversal
        iceTransportPolicy: 'all',
        iceCandidatePoolSize: 20, // Higher for better connectivity in mesh networks
        iceGatheringTimeout: 5000, // Faster gathering for local networks
    };
    
    tailscaleInfo.networkType = 'tailscale';
    updateStatusWithTailscale("Tailscale network detected");
}

function configureWebRTCTraditional() {
    // Traditional configuration with TURN servers
    webrtcConfig = {
        ...webrtcConfig,
        iceServers: [
            { urls: "stun:stun.l.google.com:19302" },
            // Add TURN server configuration here if available
            // {
            //     urls: "turn:YOUR_TURN_SERVER:3478",
            //     username: "user",
            //     credential: "password"
            // }
        ],
        iceTransportPolicy: 'all'
    };
    
    tailscaleInfo.networkType = 'traditional';
}

// --- Enhanced Logging and Status Functions ---
function updateStatus(message) {
    const networkIndicator = tailscaleInfo.enabled ? '🔗' : '🌐';
    statusDiv.textContent = `${networkIndicator} Status: ${message}`;
}

function updateStatusWithTailscale(message) {
    if (tailscaleInfo.enabled) {
        statusDiv.innerHTML = `🔗 <strong>Tailscale</strong> | ${tailscaleInfo.hostname} (${tailscaleInfo.localIP}) | ${message}`;
    } else {
        updateStatus(message);
    }
}

function logDebug(message) {
    const time = new Date().toLocaleTimeString();
    const networkPrefix = tailscaleInfo.enabled ? '[Tailscale]' : '[Traditional]';
    debugDiv.textContent += `\n[${time}] ${networkPrefix} ${message}`;
    // Auto-scroll to the bottom
    debugDiv.scrollTop = debugDiv.scrollHeight;
}

function logTailscaleInfo() {
    if (tailscaleInfo.enabled) {
        logDebug(`Tailscale Network Info:`);
        logDebug(`  Local: ${tailscaleInfo.hostname} (${tailscaleInfo.localIP})`);
        logDebug(`  Peers: ${tailscaleInfo.peers.length}`);
        tailscaleInfo.peers.forEach(peer => {
            logDebug(`    - ${peer.hostname} (${peer.ip})`);
        });
    }
}

// --- WebSocket and IonSDK Setup with Tailscale Support ---
let clientLocal = null;
let signalLocal = null;

// Connection states and reconnection logic
let connectionState = 'disconnected';
let reconnectAttempts = 0;
const maxReconnectAttempts = 5;
const reconnectInterval = 3000; // 3 seconds

async function initializeConnections() {
    logDebug(`Initializing connection to ${websocketUrl}`);
    updateStatusWithTailscale("Initializing...");

    // Detect Tailscale before creating connections
    await detectAndConfigureTailscale();
    logTailscaleInfo();

    // Create Signal and Client instances with optimized config
    signalLocal = new Signal.IonSFUJSONRPCSignal(websocketUrl);
    clientLocal = new IonSDK.Client(signalLocal, webrtcConfig);

    // Setup event listeners
    setupSignalListeners();
    setupClientListeners();
}

// --- Enhanced Event Listeners ---
function setupSignalListeners() {
    // Reset event handlers to prevent duplicates on reconnect
    signalLocal.onopen = null;
    signalLocal.onclose = null;
    signalLocal.onerror = null;
    signalLocal.onmessage = null;

    // Connection opened
    signalLocal.onopen = () => {
        connectionState = 'connected';
        reconnectAttempts = 0;
        updateStatusWithTailscale("Connected to Signaling Server");
        logDebug("WebSocket connection established");

        // Join the specified room
        clientLocal.join(roomId);
        logDebug(`Attempting to join room: ${roomId}`);
    };

    // Enhanced message handling for Tailscale info
    signalLocal.onmessage = (event) => {
        try {
            const message = JSON.parse(event.data);
            if (message.type === 'tailscale_info') {
                handleTailscaleInfo(message.data);
            }
            // Let ion-sdk handle other messages
        } catch (error) {
            // Not JSON, let ion-sdk handle it
        }
    };

    // Connection error
    signalLocal.onerror = (error) => {
        logDebug(`WebSocket Error: ${error.message || error}`);
        updateStatusWithTailscale("Signaling Connection Error");
    };

    // Connection closed - handle reconnection
    signalLocal.onclose = (event) => {
        connectionState = 'disconnected';
        const reason = event.reason || `Code: ${event.code}`;
        updateStatusWithTailscale(`Signaling Connection Closed (${reason})`);
        logDebug(`WebSocket connection closed. Reason: ${reason}`);

        // Attempt reconnection if not intentional and within limits
        if (!event.wasClean && reconnectAttempts < maxReconnectAttempts) {
            reconnectAttempts++;
            const delay = reconnectInterval * Math.pow(2, reconnectAttempts - 1);
            updateStatusWithTailscale(`Reconnecting in ${delay / 1000}s... (Attempt ${reconnectAttempts}/${maxReconnectAttempts})`);
            logDebug(`Reconnection attempt ${reconnectAttempts}/${maxReconnectAttempts} in ${delay}ms`);
            setTimeout(initializeConnections, delay);
        } else if (!event.wasClean) {
            updateStatusWithTailscale("Failed to connect after multiple attempts. Please refresh the page.");
            logDebug("Max reconnection attempts reached.");
        } else {
            logDebug("Connection closed cleanly.");
        }
    };
}

function handleTailscaleInfo(data) {
    if (data.enabled) {
        // Update our local Tailscale info with server data
        tailscaleInfo = {
            ...tailscaleInfo,
            enabled: data.enabled,
            localIP: data.localIP,
            hostname: data.hostname,
            peers: data.peers || []
        };
        
        logDebug(`Received Tailscale info from server:`);
        logDebug(`  Server: ${data.hostname} (${data.localIP})`);
        logDebug(`  Server peers: ${data.peers.length}`);
        
        // Update UI to reflect Tailscale availability
        updateStatusWithTailscale("Connected via Tailscale network");
    }
}

function setupClientListeners() {
    clientLocal.onjoin = (success, reason) => {
        if (success) {
            logDebug(`Successfully joined room: ${roomId}`);
            const networkStatus = tailscaleInfo.enabled ? 
                `via Tailscale (${tailscaleInfo.peers.length} peers available)` : 
                'via traditional networking';
            updateStatusWithTailscale(`Joined Room ${networkStatus} - Starting Media`);
            startLocalStream();
        } else {
            logDebug(`Failed to join room: ${roomId}. Reason: ${reason}`);
            updateStatusWithTailscale(`Error Joining Room: ${reason}`);
        }
    };

    clientLocal.onleave = (reason) => {
        logDebug(`Left room: ${roomId}. Reason: ${reason}`);
        updateStatusWithTailscale(`Left Room: ${reason}`);
    };

    // Handle remote tracks with network info
    clientLocal.ontrack = (track, stream) => {
        const networkInfo = tailscaleInfo.enabled ? '[via Tailscale]' : '[via Internet]';
        logDebug(`Received Track ${networkInfo}: ${track.id} [${track.kind}] from Stream: ${stream.id}`);

        stream.onremovetrack = (event) => {
            logDebug(`Track removed from stream ${stream.id}: ${event.track.id} [${event.track.kind}]`);
            removeMediaElement(stream.id, event.track.kind);
        };

        if (track.kind === "video") {
            handleRemoteVideoTrack(track, stream);
        }

        if (track.kind === "audio") {
            handleRemoteAudioTrack(track, stream);
        }
    };
}

// --- Enhanced Media Handling ---

function handleRemoteVideoTrack(track, stream) {
    track.onunmute = () => {
        const networkInfo = tailscaleInfo.enabled ? 'via Tailscale network' : 'via internet';
        logDebug(`Remote Video Track Unmuted: ${track.id} on stream ${stream.id}`);
        updateStatusWithTailscale(`Receiving video stream ${networkInfo}`);

        createOrUpdateMediaElement(track, stream);
    };

    track.onmute = () => {
        logDebug(`Remote Video Track Muted: ${track.id}`);
    };
}

function handleRemoteAudioTrack(track, stream) {
    track.onunmute = () => {
        logDebug(`Remote Audio Track Unmuted: ${track.id} on stream ${stream.id}`);
        createOrUpdateMediaElement(track, stream);
    };
    
    track.onmute = () => {
        logDebug(`Remote Audio Track Muted: ${track.id}`);
    };
}

function createOrUpdateMediaElement(track, stream) {
    const elementId = `${track.kind}-${stream.id}`;
    let mediaElement = document.getElementById(elementId);

    if (!mediaElement) {
        logDebug(`Creating ${track.kind} element for stream ${stream.id}`);
        if (track.kind === 'video') {
            // Clear "waiting" message if this is the first remote video
            const waitingMsg = remotesDiv.querySelector('.waiting-message');
            if (waitingMsg) {
                remotesDiv.removeChild(waitingMsg);
            }

            mediaElement = document.createElement("video");
            mediaElement.autoplay = true;
            mediaElement.playsInline = true;
            mediaElement.controls = false;
            mediaElement.style.maxWidth = '320px';
            mediaElement.style.margin = '5px';
            
            // Add network indicator overlay
            const networkIndicator = document.createElement("div");
            networkIndicator.className = "network-indicator";
            networkIndicator.textContent = tailscaleInfo.enabled ? "🔗 Tailscale" : "🌐 Internet";
            networkIndicator.style.cssText = `
                position: absolute;
                top: 5px;
                right: 5px;
                background: rgba(0,0,0,0.7);
                color: white;
                padding: 2px 6px;
                border-radius: 3px;
                font-size: 10px;
                z-index: 1;
            `;
            
            const container = document.createElement("div");
            container.style.position = "relative";
            container.style.display = "inline-block";
            container.appendChild(mediaElement);
            container.appendChild(networkIndicator);
            
            remotesDiv.appendChild(container);
        } else if (track.kind === 'audio') {
            mediaElement = document.createElement("audio");
            mediaElement.autoplay = true;
        }
        mediaElement.id = elementId;
    } else {
        logDebug(`Updating existing ${track.kind} element for stream ${stream.id}`);
    }

    if (mediaElement.srcObject !== stream) {
        mediaElement.srcObject = stream;
    }
}

function removeMediaElement(streamId, kind) {
    const elementId = `${kind}-${streamId}`;
    const mediaElement = document.getElementById(elementId);
    if (mediaElement) {
        logDebug(`Removing ${kind} element for stream ${streamId}`);
        mediaElement.srcObject = null;
        
        // Remove container if it exists
        const container = mediaElement.parentElement;
        if (container && container.style.position === 'relative') {
            container.remove();
        } else {
            mediaElement.remove();
        }

        if (kind === 'video' && remotesDiv.querySelectorAll('video').length === 0) {
            const waitingMsg = document.createElement("span");
            waitingMsg.className = 'waiting-message';
            waitingMsg.textContent = "Waiting for video stream...";
            waitingMsg.style.margin = "5px";
            remotesDiv.appendChild(waitingMsg);
            updateStatusWithTailscale("Video stream ended");
        }
    }
}

function startLocalStream() {
    const existingVideo = document.getElementById("local-video");
    if (existingVideo) {
        logDebug("Local stream already started.");
        return;
    }

    logDebug("Requesting camera and microphone access...");
    updateStatusWithTailscale("Requesting Media Permissions");

    // Adjust media constraints based on network type
    const mediaConstraints = {
        video: tailscaleInfo.enabled ? {
            // Higher quality for Tailscale (local network)
            width: { ideal: 1280, max: 1920 },
            height: { ideal: 720, max: 1080 },
            frameRate: { ideal: 30, max: 60 }
        } : {
            // Conservative quality for internet
            width: { ideal: 640, max: 1280 },
            height: { ideal: 480, max: 720 },
            frameRate: { ideal: 15, max: 30 }
        },
        audio: {
            echoCancellation: true,
            noiseSuppression: true,
            sampleRate: tailscaleInfo.enabled ? 48000 : 44100
        }
    };

    navigator.mediaDevices.getUserMedia(mediaConstraints)
    .then(stream => {
        logDebug("Media access granted.");
        const quality = tailscaleInfo.enabled ? "High quality (Tailscale)" : "Standard quality";
        updateStatusWithTailscale(`Publishing Local Media - ${quality}`);

        // Create local video preview
        const localVideo = document.createElement("video");
        localVideo.id = "local-video";
        localVideo.srcObject = stream;
        localVideo.autoplay = true;
        localVideo.muted = true;
        localVideo.playsInline = true;
        localVideo.controls = false;
        
        // Add quality indicator
        const qualityIndicator = document.createElement("div");
        qualityIndicator.textContent = tailscaleInfo.enabled ? "🔗 High Quality" : "🌐 Standard";
        qualityIndicator.style.cssText = `
            position: absolute;
            bottom: 5px;
            left: 5px;
            background: rgba(0,0,0,0.7);
            color: white;
            padding: 2px 6px;
            border-radius: 3px;
            font-size: 10px;
        `;
        
        localDiv.innerHTML = '';
        localDiv.style.position = 'relative';
        localDiv.appendChild(localVideo);
        localDiv.appendChild(qualityIndicator);

        // Publish tracks to ion-sfu
        stream.getTracks().forEach(track => {
            logDebug(`Publishing local ${track.kind} track: ${track.id}`);
            if (clientLocal && typeof clientLocal.publish === 'function') {
                clientLocal.publish(track);
            } else {
                logDebug("Error: clientLocal not initialized or missing publish method.");
                updateStatusWithTailscale("Error: Could not publish media.");
            }

            track.onended = () => {
                logDebug(`Local ${track.kind} track ended`);
                updateStatusWithTailscale(`Local ${track.kind} feed stopped.`);
                if (localVideo) localVideo.remove();
            };
        });
    })
    .catch(err => {
        logDebug(`getUserMedia Error: ${err.name} - ${err.message}`);
        updateStatusWithTailscale(`Media Access Error: ${err.message}`);
        localDiv.innerHTML = `<span style="color: red;">Could not access camera/mic: ${err.message}</span>`;
    });
}

// --- Network Monitoring ---
function startNetworkMonitoring() {
    // Monitor connection quality
    setInterval(() => {
        if (clientLocal && connectionState === 'connected') {
            // Could add RTCPeerConnection stats monitoring here
            // For now, just check if we're still connected
            if (signalLocal.readyState === WebSocket.CLOSED) {
                logDebug("Connection lost detected by monitoring");
                updateStatusWithTailscale("Connection lost - attempting reconnection");
                if (reconnectAttempts < maxReconnectAttempts) {
                    initializeConnections();
                }
            }
        }
    }, 10000); // Check every 10 seconds
}

// --- Page Lifecycle and Initialization ---

document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
        if (connectionState === "disconnected" && reconnectAttempts >= maxReconnectAttempts) {
            logDebug("Page visible again, max reconnect attempts reached. Manual refresh needed.");
        } else if (connectionState === "disconnected") {
            logDebug("Page visible again, attempting to reconnect...");
            reconnectAttempts = 0;
            initializeConnections();
        }
    }
});

// Start the connection process when the page loads
window.addEventListener("load", () => {
    logDebug("Page loaded. Starting Tailscale-enhanced application.");
    initializeConnections();
    startNetworkMonitoring();
});