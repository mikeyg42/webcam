// Configuration Management JavaScript

let currentConfig = null;

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
    loadConfig();

    // Setup form submission
    document.getElementById('configForm').addEventListener('submit', async (e) => {
        e.preventDefault();
        await saveConfig();
    });
});

// Switch between tabs
function switchTab(tabName) {
    // Update tab buttons
    const tabs = document.querySelectorAll('.tab');
    tabs.forEach(tab => tab.classList.remove('active'));
    event.target.classList.add('active');

    // Update tab content
    const contents = document.querySelectorAll('.tab-content');
    contents.forEach(content => content.classList.remove('active'));
    document.getElementById(`${tabName}-tab`).classList.add('active');
}

// Load configuration from API
async function loadConfig() {
    showLoading(true);
    showAlert('', 'hide');

    try {
        const response = await fetch('/api/config');
        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }

        currentConfig = await response.json();
        populateForm(currentConfig);
        showLoading(false);

    } catch (error) {
        console.error('Error loading config:', error);
        showAlert(`Failed to load configuration: ${error.message}`, 'error');
        showLoading(false);
    }
}

// Save configuration to API
async function saveConfig() {
    showAlert('', 'hide');

    // Collect form data
    const config = {
        recording: {
            continuousEnabled: document.getElementById('recordingMode').value === 'continuous' ||
                             document.getElementById('recordingMode').value === 'both',
            eventEnabled: document.getElementById('recordingMode').value === 'event' ||
                        document.getElementById('recordingMode').value === 'both',
            saveDirectory: document.getElementById('saveDirectory').value,
            segmentDuration: parseInt(document.getElementById('segmentDuration').value),
            preMotionBuffer: parseInt(document.getElementById('preMotionBuffer').value),
            postMotionBuffer: parseInt(document.getElementById('postMotionBuffer').value),
            retentionDays: parseInt(document.getElementById('retentionDays').value)
        },
        video: {
            width: parseInt(document.getElementById('videoWidth').value),
            height: parseInt(document.getElementById('videoHeight').value),
            framerate: parseInt(document.getElementById('framerate').value),
            bitrate: parseInt(document.getElementById('bitrate').value)
        },
        motion: {
            enabled: true,
            threshold: parseFloat(document.getElementById('motionThreshold').value),
            minimumArea: parseInt(document.getElementById('minimumArea').value),
            cooldownPeriod: parseInt(document.getElementById('cooldownPeriod').value),
            noMotionDelay: parseInt(document.getElementById('noMotionDelay').value),
            minConsecutiveFrames: parseInt(document.getElementById('minConsecutiveFrames').value),
            frameSkip: parseInt(document.getElementById('frameSkip').value)
        },
        storage: {
            minio: {
                endpoint: document.getElementById('minioEndpoint').value,
                bucket: document.getElementById('minioBucket').value,
                accessKeyId: document.getElementById('minioAccessKey').value,
                secretAccessKey: document.getElementById('minioSecretKey').value,
                useSSL: document.getElementById('minioUseSSL').checked
            },
            postgres: {
                host: document.getElementById('postgresHost').value,
                port: parseInt(document.getElementById('postgresPort').value),
                database: document.getElementById('postgresDatabase').value,
                username: document.getElementById('postgresUsername').value,
                password: document.getElementById('postgresPassword').value,
                sslMode: 'disable'
            }
        },
        tailscale: {
            enabled: document.getElementById('tailscaleEnabled').checked,
            nodeName: document.getElementById('tailscaleNodeName').value,
            hostname: document.getElementById('tailscaleHostname').value,
            listenPort: parseInt(document.getElementById('tailscalePort').value),
            authKey: document.getElementById('tailscaleAuthKey').value
        },
        email: {
            method: document.getElementById('emailMethod').value,
            toEmail: document.getElementById('emailTo').value,
            fromEmail: document.getElementById('emailFrom').value,
            mailsendApiToken: document.getElementById('mailersendToken').value,
            gmailClientId: document.getElementById('gmailClientId').value,
            gmailClientSecret: document.getElementById('gmailClientSecret').value
        },
        webrtc: {
            username: document.getElementById('webrtcUsername').value,
            password: document.getElementById('webrtcPassword').value
        }
    };

    try {
        const response = await fetch('/api/config', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify(config)
        });

        if (!response.ok) {
            const errorData = await response.json();
            throw new Error(errorData.error || `HTTP error! status: ${response.status}`);
        }

        const result = await response.json();
        showAlert(result.message || 'Configuration saved successfully!', 'success');

        // Reload config to get updated values
        setTimeout(() => loadConfig(), 1000);

    } catch (error) {
        console.error('Error saving config:', error);
        showAlert(`Failed to save configuration: ${error.message}`, 'error');
    }
}

// Populate form with configuration data
function populateForm(config) {
    // Recording settings
    if (config.recording.continuousEnabled && config.recording.eventEnabled) {
        document.getElementById('recordingMode').value = 'both';
    } else if (config.recording.continuousEnabled) {
        document.getElementById('recordingMode').value = 'continuous';
    } else {
        document.getElementById('recordingMode').value = 'event';
    }

    document.getElementById('saveDirectory').value = config.recording.saveDirectory || '';
    document.getElementById('segmentDuration').value = config.recording.segmentDuration || 5;
    document.getElementById('preMotionBuffer').value = config.recording.preMotionBuffer || 10;
    document.getElementById('postMotionBuffer').value = config.recording.postMotionBuffer || 30;
    document.getElementById('retentionDays').value = config.recording.retentionDays || 7;

    // Video settings
    document.getElementById('videoWidth').value = config.video.width || 640;
    document.getElementById('videoHeight').value = config.video.height || 480;
    document.getElementById('framerate').value = config.video.framerate || 25;
    document.getElementById('bitrate').value = config.video.bitrate || 500000;

    // Motion settings
    document.getElementById('motionThreshold').value = config.motion.threshold || 20;
    document.getElementById('minimumArea').value = config.motion.minimumArea || 500;
    document.getElementById('cooldownPeriod').value = config.motion.cooldownPeriod || 10;
    document.getElementById('noMotionDelay').value = config.motion.noMotionDelay || 3;
    document.getElementById('minConsecutiveFrames').value = config.motion.minConsecutiveFrames || 2;
    document.getElementById('frameSkip').value = config.motion.frameSkip || 2;

    // Storage settings
    document.getElementById('minioEndpoint').value = config.storage.minio.endpoint || 'localhost:9000';
    document.getElementById('minioBucket').value = config.storage.minio.bucket || 'recordings';
    document.getElementById('minioAccessKey').value = config.storage.minio.accessKeyId || '';
    document.getElementById('minioUseSSL').checked = config.storage.minio.useSSL || false;

    document.getElementById('postgresHost').value = config.storage.postgres.host || 'localhost';
    document.getElementById('postgresPort').value = config.storage.postgres.port || 5432;
    document.getElementById('postgresDatabase').value = config.storage.postgres.database || 'recordings';
    document.getElementById('postgresUsername').value = config.storage.postgres.username || 'recorder';

    // Tailscale settings
    document.getElementById('tailscaleEnabled').checked = config.tailscale.enabled || false;
    document.getElementById('tailscaleNodeName').value = config.tailscale.nodeName || '';
    document.getElementById('tailscaleHostname').value = config.tailscale.hostname || '';
    document.getElementById('tailscalePort').value = config.tailscale.listenPort || 41641;
    updateTailscaleFields();

    // Email settings
    document.getElementById('emailMethod').value = config.email.method || 'disabled';
    document.getElementById('emailTo').value = config.email.toEmail || '';
    document.getElementById('emailFrom').value = config.email.fromEmail || '';
    updateEmailFields();

    // WebRTC settings
    document.getElementById('webrtcUsername').value = config.webrtc.username || '';

    // Show the form
    document.getElementById('configForm').style.display = 'block';
}

// Update recording mode visibility
function updateRecordingMode() {
    const mode = document.getElementById('recordingMode').value;
    // Could add conditional field visibility based on mode if needed
}

// Update Tailscale field visibility
function updateTailscaleFields() {
    const enabled = document.getElementById('tailscaleEnabled').checked;
    const fields = document.getElementById('tailscaleFields');
    fields.style.opacity = enabled ? '1' : '0.5';

    // Disable/enable inputs
    const inputs = fields.querySelectorAll('input');
    inputs.forEach(input => input.disabled = !enabled);
}

// Update email field visibility
function updateEmailFields() {
    const method = document.getElementById('emailMethod').value;

    const commonFields = document.getElementById('emailCommonFields');
    const mailersendFields = document.getElementById('mailersendFields');
    const gmailFields = document.getElementById('gmailFields');

    commonFields.style.display = method !== 'disabled' ? 'block' : 'none';
    mailersendFields.style.display = method === 'mailersend' ? 'block' : 'none';
    gmailFields.style.display = method === 'gmail' ? 'block' : 'none';
}

// Set video preset
function setVideoPreset(preset) {
    const presets = {
        low: { width: 320, height: 240, framerate: 15, bitrate: 200000 },
        medium: { width: 640, height: 480, framerate: 25, bitrate: 500000 },
        high: { width: 1280, height: 720, framerate: 30, bitrate: 2000000 }
    };

    const settings = presets[preset];
    if (settings) {
        document.getElementById('videoWidth').value = settings.width;
        document.getElementById('videoHeight').value = settings.height;
        document.getElementById('framerate').value = settings.framerate;
        document.getElementById('bitrate').value = settings.bitrate;

        showAlert(`Applied ${preset} quality preset`, 'success');
        setTimeout(() => showAlert('', 'hide'), 2000);
    }
}

// Show/hide loading spinner
function showLoading(show) {
    document.getElementById('loading').style.display = show ? 'block' : 'none';
    document.getElementById('configForm').style.display = show ? 'none' : 'block';
}

// Show alert message
function showAlert(message, type) {
    const container = document.getElementById('alertContainer');

    if (type === 'hide') {
        container.innerHTML = '';
        return;
    }

    const alertDiv = document.createElement('div');
    alertDiv.className = `alert ${type} show`;
    alertDiv.textContent = message;
    alertDiv.style.margin = '20px 30px 0';

    container.innerHTML = '';
    container.appendChild(alertDiv);

    // Auto-hide success messages after 5 seconds
    if (type === 'success') {
        setTimeout(() => showAlert('', 'hide'), 5000);
    }
}
