const remotesDiv = document.getElementById("remotes");
const statusDiv = document.getElementById("status");
const debugDiv = document.getElementById("debug");
const streamInfoDiv = document.getElementById("stream-info");

// --- Configuration ---

// Get Room ID from URL query parameter (e.g., ?roomId=myRoom)
const urlParams = new URLSearchParams(window.location.search);
const roomId = urlParams.get('roomId') || 'cameraRoom'; // Use a default if none provided

// Get server address from current location
const serverProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
const serverHost = window.location.host;
const websocketUrl = `${serverProtocol}//${serverHost}/ws?roomId=${encodeURIComponent(roomId)}`;

// WebRTC Configuration - Will be loaded dynamically from server
let webrtcConfig = {
    codec: 'vp9',
    iceServers: [
        // Fallback STUN server (in case config fetch fails)
        { urls: "stun:stun.l.google.com:19302" }
    ],
    iceTransportPolicy: 'all'
};

document.title = `Security Camera Viewer - Room: ${roomId}`;

// --- Logging and Status Functions ---
function updateStatus(message) {
    statusDiv.textContent = `Status: ${message}`;
}

function logDebug(message) {
    const time = new Date().toLocaleTimeString();
    debugDiv.textContent += `\n[${time}] ${message}`;
    // Auto-scroll to the bottom
    debugDiv.scrollTop = debugDiv.scrollHeight;
}

function updateStreamInfo(message) {
    streamInfoDiv.innerHTML = `<h3>Security Camera Feed</h3><p>${message}</p>`;
}

// --- WebSocket and IonSDK Setup ---
let clientLocal = null;
let signalLocal = null;

// Connection states and reconnection logic
let connectionState = 'disconnected';
let reconnectAttempts = 0;
const maxReconnectAttempts = 5;
const reconnectInterval = 3000; // 3 seconds

// Function to fetch WebRTC configuration from server
async function loadWebRTCConfig() {
    try {
        logDebug("Loading WebRTC configuration from server...");
        const response = await fetch('/api/webrtc-config');
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }
        const config = await response.json();
        webrtcConfig = config;
        logDebug(`WebRTC config loaded: ${webrtcConfig.iceServers.length} ICE servers`);
        return true;
    } catch (error) {
        logDebug(`Failed to load WebRTC config: ${error.message}. Using fallback configuration.`);
        return false;
    }
}

async function initializeConnections() {
    logDebug(`Initializing viewer connection to ${websocketUrl}`);
    updateStatus("Connecting to security camera...");
    updateStreamInfo("Connecting to security camera system...");

    // Load WebRTC configuration from server first
    await loadWebRTCConfig();

    // Create Signal and Client instances for receiving only
    signalLocal = new Signal.IonSFUJSONRPCSignal(websocketUrl);
    clientLocal = new IonSDK.Client(signalLocal, webrtcConfig);

    // Setup event listeners
    setupSignalListeners();
    setupClientListeners();
}

// --- Event Listeners ---
function setupSignalListeners() {
    // Reset event handlers to prevent duplicates on reconnect
    signalLocal.onopen = null;
    signalLocal.onclose = null;
    signalLocal.onerror = null;

    // Connection opened
    signalLocal.onopen = () => {
        connectionState = 'connected';
        reconnectAttempts = 0;
        updateStatus("Connected to Security Camera System");
        updateStreamInfo("Connected - waiting for camera feed...");
        logDebug("WebSocket connection established");

        // Join the specified room as viewer only
        clientLocal.join(roomId);
        logDebug(`Joining room as viewer: ${roomId}`);
    };

    // Connection error
    signalLocal.onerror = (error) => {
        logDebug(`WebSocket Error: ${error.message || error}`);
        updateStatus("Connection Error");
        updateStreamInfo("Connection error - retrying...");
    };

    // Connection closed - handle reconnection
    signalLocal.onclose = (event) => {
        connectionState = 'disconnected';
        const reason = event.reason || `Code: ${event.code}`;
        updateStatus(`Connection Lost (${reason})`);
        updateStreamInfo("Connection lost - attempting to reconnect...");
        logDebug(`WebSocket connection closed. Reason: ${reason}`);

        // Clear any existing video streams
        clearVideoStreams();

        // Attempt reconnection if not intentional and within limits
        if (!event.wasClean && reconnectAttempts < maxReconnectAttempts) {
            reconnectAttempts++;
            const delay = reconnectInterval * Math.pow(2, reconnectAttempts - 1); // Exponential backoff
            updateStatus(`Reconnecting in ${delay / 1000}s... (Attempt ${reconnectAttempts}/${maxReconnectAttempts})`);
            updateStreamInfo(`Reconnecting in ${delay / 1000} seconds...`);
            logDebug(`Reconnection attempt ${reconnectAttempts}/${maxReconnectAttempts} in ${delay}ms`);
            setTimeout(initializeConnections, delay);
        } else if (!event.wasClean) {
            updateStatus("Connection failed after multiple attempts");
            updateStreamInfo("Connection failed. Please refresh the page.");
            logDebug("Max reconnection attempts reached.");
        } else {
            logDebug("Connection closed cleanly.");
        }
    };
}

function setupClientListeners() {
    clientLocal.onjoin = (success, reason) => {
        if (success) {
            logDebug(`Successfully joined room as viewer: ${roomId}`);
            updateStatus("Waiting for Security Camera Stream");
            updateStreamInfo("Connected - waiting for camera to start streaming...");
        } else {
            logDebug(`Failed to join room: ${roomId}. Reason: ${reason}`);
            updateStatus(`Error Joining Room: ${reason}`);
            updateStreamInfo(`Error joining room: ${reason}`);
        }
    };

    clientLocal.onleave = (reason) => {
        logDebug(`Left room: ${roomId}. Reason: ${reason}`);
        updateStatus(`Left Room: ${reason}`);
        updateStreamInfo("Left room");
        clearVideoStreams();
    };

    // Handle remote tracks (this is where the security camera stream will appear)
    clientLocal.ontrack = (track, stream) => {
        logDebug(`Received Track: ${track.id} [${track.kind}] from Stream: ${stream.id}`);

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

// --- Media Handling (Receive Only) ---

function handleRemoteVideoTrack(track, stream) {
    track.onunmute = () => {
        logDebug(`Security Camera Video Track Active: ${track.id} on stream ${stream.id}`);
        updateStatus("Receiving security camera video");
        updateStreamInfo("Security camera video stream active");

        // Create video element for security camera
        createVideoElement(track, stream);
    };

    track.onmute = () => {
        logDebug(`Security Camera Video Track Muted: ${track.id}`);
        updateStreamInfo("Security camera video paused");
    };

    track.onended = () => {
        logDebug(`Security Camera Video Track Ended: ${track.id}`);
        updateStreamInfo("Security camera video stopped");
        removeMediaElement(stream.id, track.kind);
    };
}

function handleRemoteAudioTrack(track, stream) {
    track.onunmute = () => {
        logDebug(`Security Camera Audio Track Active: ${track.id} on stream ${stream.id}`);
        // Create audio element for security camera
        createAudioElement(track, stream);
    };

    track.onmute = () => {
        logDebug(`Security Camera Audio Track Muted: ${track.id}`);
    };

    track.onended = () => {
        logDebug(`Security Camera Audio Track Ended: ${track.id}`);
        removeMediaElement(stream.id, track.kind);
    };
}

function createVideoElement(track, stream) {
    const elementId = `video-${stream.id}`;
    let videoElement = document.getElementById(elementId);

    if (!videoElement) {
        logDebug(`Creating video element for security camera stream ${stream.id}`);

        videoElement = document.createElement("video");
        videoElement.id = elementId;
        videoElement.autoplay = true;
        videoElement.playsInline = true;
        videoElement.controls = true; // Allow user to control playback
        videoElement.muted = false; // Don't mute the security camera feed

        // Clear the waiting message and add the video
        remotesDiv.innerHTML = '';
        remotesDiv.appendChild(videoElement);
    } else {
        logDebug(`Updating existing video element for stream ${stream.id}`);
    }

    // Assign the stream to the element
    if (videoElement.srcObject !== stream) {
        videoElement.srcObject = stream;
    }
}

function createAudioElement(track, stream) {
    const elementId = `audio-${stream.id}`;
    let audioElement = document.getElementById(elementId);

    if (!audioElement) {
        logDebug(`Creating audio element for security camera stream ${stream.id}`);

        audioElement = document.createElement("audio");
        audioElement.id = elementId;
        audioElement.autoplay = true;
        audioElement.controls = false; // Hidden audio controls

        // Add to page (but hidden)
        document.body.appendChild(audioElement);
    }

    // Assign the stream to the element
    if (audioElement.srcObject !== stream) {
        audioElement.srcObject = stream;
    }
}

function removeMediaElement(streamId, kind) {
    const elementId = `${kind}-${streamId}`;
    const mediaElement = document.getElementById(elementId);
    if (mediaElement) {
        logDebug(`Removing ${kind} element for stream ${streamId}`);
        mediaElement.srcObject = null; // Release stream
        mediaElement.remove();

        // If it was a video and no other videos are left, show waiting message
        if (kind === 'video' && remotesDiv.querySelectorAll('video').length === 0) {
            updateStreamInfo("Waiting for security camera stream...");
            updateStatus("Video stream ended");
        }
    }
}

function clearVideoStreams() {
    // Remove all video and audio elements
    remotesDiv.innerHTML = '';
    const audioElements = document.querySelectorAll('audio');
    audioElements.forEach(audio => {
        audio.srcObject = null;
        audio.remove();
    });
    updateStreamInfo("No active streams");
}

// --- Page Lifecycle and Initialization ---

document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") {
        if (connectionState === "disconnected" && reconnectAttempts >= maxReconnectAttempts) {
            logDebug("Page visible again, max reconnect attempts reached. Manual refresh needed.");
        } else if (connectionState === "disconnected") {
            logDebug("Page visible again, attempting to reconnect...");
            // Reset attempts and try connecting again immediately
            reconnectAttempts = 0;
            initializeConnections();
        }
    }
});

// Start the connection process when the page loads
window.addEventListener("load", () => {
    logDebug("Security Camera Viewer loaded. Starting connection.");
    updateStreamInfo("Initializing security camera viewer...");
    initializeConnections();
});