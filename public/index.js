const remotesDiv = document.getElementById("remotes");
const localDiv = document.getElementById("local");
const statusDiv = document.getElementById("status");
const debugDiv = document.getElementById("debug");

// --- Configuration ---

// Get Room ID from URL query parameter (e.g., ?roomId=myRoom)
const urlParams = new URLSearchParams(window.location.search);
const roomId = urlParams.get('roomId') || 'defaultRoom'; // Use a default if none provided

// Get server address from current location
const serverProtocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
const serverHost = window.location.host;
const websocketUrl = `${serverProtocol}//${serverHost}/ws?roomId=${encodeURIComponent(roomId)}`;

// WebRTC Configuration - IMPORTANT: Replace placeholders with your actual TURN server details
const webrtcConfig = {
    codec: 'vp8',
    iceServers: [
        // Public STUN server (for NAT traversal assistance)
        { urls: "stun:stun.l.google.com:19302" },
        // Add your TURN server configuration here
        {
            urls: "turn:YOUR_TURN_SERVER_PUBLIC_IP_OR_DOMAIN:3478?transport=udp",
            username: "turnuser",         // Replace with username from sfu.toml
            credential: "turnpassword"   // Replace with password from sfu.toml
        },
        {
            urls: "turn:YOUR_TURN_SERVER_PUBLIC_IP_OR_DOMAIN:3478?transport=tcp",
            username: "turnuser",
            credential: "turnpassword"
        }
        // If using TLS/DTLS for TURN (recommended):
        // { urls: "turns:YOUR_TURN_SERVER_PUBLIC_IP_OR_DOMAIN:5349?transport=tcp" ... }
    ],
    iceTransportPolicy: 'relay' // Consider 'all' for testing, 'relay' forces TURN usage
};

document.title = `Webcam Viewer - Room: ${roomId}`;

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

// --- WebSocket and IonSDK Setup ---
let clientLocal = null;
let signalLocal = null;

// Connection states and reconnection logic
let connectionState = 'disconnected';
let reconnectAttempts = 0;
const maxReconnectAttempts = 5;
const reconnectInterval = 3000; // 3 seconds

function initializeConnections() {
    logDebug(`Initializing connection to ${websocketUrl}`);
    updateStatus("Initializing...");

    // Create Signal and Client instances
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
        updateStatus("Connected to Signaling Server");
        logDebug("WebSocket connection established");

        // Join the specified room
        clientLocal.join(roomId);
        logDebug(`Attempting to join room: ${roomId}`);
    };

    // Connection error
    signalLocal.onerror = (error) => {
        logDebug(`WebSocket Error: ${error.message || error}`);
        updateStatus("Signaling Connection Error");
        // Close event will likely trigger reconnection logic
    };

    // Connection closed - handle reconnection
    signalLocal.onclose = (event) => {
        connectionState = 'disconnected';
        const reason = event.reason || `Code: ${event.code}`;
        updateStatus(`Signaling Connection Closed (${reason})`);
        logDebug(`WebSocket connection closed. Reason: ${reason}`);

        // Attempt reconnection if not intentional and within limits
        if (!event.wasClean && reconnectAttempts < maxReconnectAttempts) {
            reconnectAttempts++;
            const delay = reconnectInterval * Math.pow(2, reconnectAttempts -1); // Exponential backoff
            updateStatus(`Reconnecting in ${delay / 1000}s... (Attempt ${reconnectAttempts}/${maxReconnectAttempts})`);
            logDebug(`Reconnection attempt ${reconnectAttempts}/${maxReconnectAttempts} in ${delay}ms`);
            setTimeout(initializeConnections, delay);
        } else if (!event.wasClean) {
            updateStatus("Failed to connect after multiple attempts. Please refresh the page.");
            logDebug("Max reconnection attempts reached.");
        } else {
            logDebug("Connection closed cleanly.");
        }
    };
}

function setupClientListeners() {
    clientLocal.onjoin = (success, reason) => {
        if (success) {
            logDebug(`Successfully joined room: ${roomId}`);
            updateStatus("Joined Room - Starting Media");
            // Get user media and publish
            startLocalStream();
        } else {
            logDebug(`Failed to join room: ${roomId}. Reason: ${reason}`);
            updateStatus(`Error Joining Room: ${reason}`);
            // Consider closing the connection or showing an error message
        }
    };

    clientLocal.onleave = (reason) => {
        logDebug(`Left room: ${roomId}. Reason: ${reason}`);
        updateStatus(`Left Room: ${reason}`);
        // Clean up media elements?
    };

    // Handle remote tracks
    clientLocal.ontrack = (track, stream) => {
        logDebug(`Received Track: ${track.id} [${track.kind}] from Stream: ${stream.id}`);

        stream.onremovetrack = (event) => {
             logDebug(`Track removed from stream ${stream.id}: ${event.track.id} [${event.track.kind}]`);
             // Find media element associated with this stream/track and remove it
             removeMediaElement(stream.id, event.track.kind);
        };

        if (track.kind === "video") {
            handleRemoteVideoTrack(track, stream);
        }

        if (track.kind === "audio") {
            handleRemoteAudioTrack(track, stream);
        }
    };

    // Handle DataChannel messages if needed
    // clientLocal.ondatachannel = (datachannel) => { ... };
    // clientLocal.onmessage = (message) => { ... };
}

// --- Media Handling ---

function handleRemoteVideoTrack(track, stream) {
    track.onunmute = () => {
        logDebug(`Remote Video Track Unmuted: ${track.id} on stream ${stream.id}`);
        updateStatus("Receiving video stream");

        // Create video element
        createOrUpdateMediaElement(track, stream);
    };

    track.onmute = () => {
        logDebug(`Remote Video Track Muted: ${track.id}`);
        // Optionally indicate the mute state in the UI
    };
}

function handleRemoteAudioTrack(track, stream) {
    track.onunmute = () => {
        logDebug(`Remote Audio Track Unmuted: ${track.id} on stream ${stream.id}`);
        // Create audio element
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
            mediaElement.playsInline = true; // Important for mobile
            mediaElement.controls = false;
            mediaElement.style.maxWidth = '320px'; // Example styling
            mediaElement.style.margin = '5px';
            remotesDiv.appendChild(mediaElement);
        } else if (track.kind === 'audio') {
            mediaElement = document.createElement("audio");
            mediaElement.autoplay = true;
            // Don't add audio elements to the visible DOM unless controls are needed
            // document.body.appendChild(mediaElement);
        }
        mediaElement.id = elementId;

    } else {
         logDebug(`Updating existing ${track.kind} element for stream ${stream.id}`);
    }

    // Assign the stream to the element
    // Check if srcObject is already set to this stream to avoid unnecessary updates
    if (mediaElement.srcObject !== stream) {
        mediaElement.srcObject = stream;
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
             const waitingMsg = document.createElement("span");
             waitingMsg.className = 'waiting-message';
             waitingMsg.textContent = "Waiting for video stream...";
             waitingMsg.style.margin = "5px";
             remotesDiv.appendChild(waitingMsg);
             updateStatus("Video stream ended");
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
    updateStatus("Requesting Media Permissions");

    navigator.mediaDevices.getUserMedia({
        video: {
            width: { ideal: 640 }, // Lower resolution for preview/publish
            height: { ideal: 480 },
            frameRate: { ideal: 15 } // Lower frame rate
        },
        audio: true
    })
    .then(stream => {
        logDebug("Media access granted.");
        updateStatus("Publishing Local Media");

        // Create local video preview
        const localVideo = document.createElement("video");
        localVideo.id = "local-video";
        localVideo.srcObject = stream;
        localVideo.autoplay = true;
        localVideo.muted = true; // Mute local preview
        localVideo.playsInline = true;
        localVideo.controls = false;
        localDiv.innerHTML = ''; // Clear any previous placeholder
        localDiv.appendChild(localVideo);

        // Publish tracks to ion-sfu
        stream.getTracks().forEach(track => {
             logDebug(`Publishing local ${track.kind} track: ${track.id}`);
             // Add track to the client for publishing
             // Check if clientLocal exists and has the publish method
             if (clientLocal && typeof clientLocal.publish === 'function') {
                 clientLocal.publish(track);
             } else {
                 logDebug("Error: clientLocal not initialized or missing publish method.");
                 updateStatus("Error: Could not publish media.");
             }

            // Handle track ending (e.g., user stops sharing)
            track.onended = () => {
                logDebug(`Local ${track.kind} track ended`);
                updateStatus(`Local ${track.kind} feed stopped.`);
                // Optionally try to remove the track from the peer connection or notify others
                if (localVideo) localVideo.remove(); // Remove preview
            };
        });
    })
    .catch(err => {
        logDebug(`getUserMedia Error: ${err.name} - ${err.message}`);
        updateStatus(`Media Access Error: ${err.message}`);
        localDiv.innerHTML = `<span style="color: red;">Could not access camera/mic: ${err.message}</span>`;
    });
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
    logDebug("Page loaded. Starting application.");
    initializeConnections();
});