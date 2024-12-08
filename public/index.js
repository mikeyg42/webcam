const remotesDiv = document.getElementById("remotes");
const statusDiv = document.getElementById("status");
const debugDiv = document.getElementById("debug");

function updateStatus(message) {
    statusDiv.textContent = `Connection Status: ${message}`;
}

function logDebug(message) {
    const time = new Date().toLocaleTimeString();
    debugDiv.textContent += `\n[${time}] ${message}`;
    debugDiv.scrollTop = debugDiv.scrollHeight;
}

const config = {
    codec: 'vp8',
    iceServers: [
        {
            "urls": "stun:stun.l.google.com:19302",
        }
    ]
};

updateStatus("Connecting to server...");

const signalLocal = new Signal.IonSFUJSONRPCSignal(
    "ws://127.0.0.1:7000/ws"
);

const clientLocal = new IonSDK.Client(signalLocal, config);

signalLocal.onopen = () => {
    updateStatus("Connected to server");
    logDebug("WebSocket connection established");
    clientLocal.join("test room");
    logDebug("Joined test room");
};

clientLocal.ontrack = (track, stream) => {
    logDebug(`Got track: ${track.id} (${track.kind})`);
    
    if (track.kind === "video") {
        track.onunmute = () => {
            logDebug("Video track unmuted");
            // Clear "waiting" message
            remotesDiv.innerHTML = '';
            
            const remoteVideo = document.createElement("video");
            remoteVideo.srcObject = stream;
            remoteVideo.autoplay = true;
            remoteVideo.muted = true;
            remotesDiv.appendChild(remoteVideo);
            
            updateStatus("Receiving video stream");

            track.onremovetrack = () => {
                remotesDiv.removeChild(remoteVideo);
                updateStatus("Video stream ended");
                logDebug("Video track removed");
            };
        };
    }
};

signalLocal.onerror = (error) => {
    updateStatus("Connection error");
    logDebug(`Error: ${error}`);
};

signalLocal.onclose = () => {
    updateStatus("Connection closed");
    logDebug("WebSocket connection closed");
};