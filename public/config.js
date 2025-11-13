const API_BASE_URL = 'http://localhost:8081';
let currentConfig = null;

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
    loadConfig();

    // Setup form submission
    document.getElementById('configForm').addEventListener('submit', async (e) => {
        e.preventDefault();
        await saveConfig();
    });

    // Email method change handler
    document.getElementById('emailMethod').addEventListener('change', (e) => {
        const emailFields = document.getElementById('emailFields');
        emailFields.style.display = e.target.value !== 'disabled' ? 'block' : 'none';
    });

    // Tailscale toggle handler
    document.getElementById('tailscaleEnabled').addEventListener('change', (e) => {
        const tailscaleFields = document.getElementById('tailscaleFields');
        tailscaleFields.style.display = e.target.checked ? 'block' : 'none';
    });
});

// Load configuration from API
async function loadConfig() {
    console.log('[Config] Starting to load configuration...');
    showLoading(true);
    showAlert('', 'hide');

    try {
        console.log('[Config] Fetching from:', `${API_BASE_URL}/api/config`);
        const response = await fetch(`${API_BASE_URL}/api/config`);
        console.log('[Config] Response status:', response.status);

        if (!response.ok) {
            throw new Error(`HTTP error! status: ${response.status}`);
        }

        currentConfig = await response.json();
        console.log('[Config] Received config:', currentConfig);

        populateForm(currentConfig);
        console.log('[Config] Form populated successfully');

        showLoading(false);
        console.log('[Config] Loading complete');

    } catch (error) {
        console.error('[Config] Error loading config:', error);
        showAlert(`Failed to load configuration: ${error.message}`, 'error');
        showLoading(false);
    }
}

// Populate form with config data
function populateForm(config) {
    console.log('[Config] Populating form with config:', config);
    try {
        // Video
        console.log('[Config] Setting video fields...');
        document.getElementById('videoWidth').value = config.video?.width || 640;
        document.getElementById('videoHeight').value = config.video?.height || 480;
        document.getElementById('framerate').value = config.video?.framerate || 25;
        document.getElementById('bitrate').value = config.video?.bitrate || 500000;

    // Recording
    document.getElementById('saveDirectory').value = config.recording?.saveDirectory || 'recordings/';
    document.getElementById('eventRecording').checked = config.recording?.eventEnabled !== false;
    document.getElementById('continuousRecording').checked = config.recording?.continuousEnabled === true;
    document.getElementById('preMotionBuffer').value = config.recording?.preMotionBuffer || 10;
    document.getElementById('postMotionBuffer').value = config.recording?.postMotionBuffer || 30;

    // Motion
    document.getElementById('motionThreshold').value = config.motion?.threshold || 20;
    document.getElementById('minimumArea').value = config.motion?.minimumArea || 500;
    document.getElementById('cooldownPeriod').value = config.motion?.cooldownPeriod || 10;
    document.getElementById('noMotionDelay').value = config.motion?.noMotionDelay || 3;
    document.getElementById('minConsecutiveFrames').value = config.motion?.minConsecutiveFrames || 2;
    document.getElementById('frameSkip').value = config.motion?.frameSkip || 2;

    // Storage
    document.getElementById('minioEndpoint').value = config.storage?.minio?.endpoint || 'localhost:9000';
    document.getElementById('minioBucket').value = config.storage?.minio?.bucket || 'recordings';
    document.getElementById('minioAccessKey').value = config.storage?.minio?.accessKeyId || 'minioadmin';
    document.getElementById('minioSecretKey').value = config.storage?.minio?.secretAccessKey || 'minioadmin';
    document.getElementById('minioUseSSL').checked = config.storage?.minio?.useSSL === true;

    document.getElementById('postgresHost').value = config.storage?.postgres?.host || 'localhost';
    document.getElementById('postgresPort').value = config.storage?.postgres?.port || 5432;
    document.getElementById('postgresDatabase').value = config.storage?.postgres?.database || 'recordings';
    document.getElementById('postgresUsername').value = config.storage?.postgres?.username || 'recorder';
    document.getElementById('postgresPassword').value = config.storage?.postgres?.password || 'recorder';

    // Network
    document.getElementById('webrtcUsername').value = config.webrtc?.username || 'testuser';
    // Don't populate password for security
    document.getElementById('tailscaleEnabled').checked = config.tailscale?.enabled === true;
    document.getElementById('tailscaleNodeName').value = config.tailscale?.nodeName || 'webcam-security';
    document.getElementById('tailscalePort').value = config.tailscale?.listenPort || 41641;

    // Show/hide tailscale fields
    const tailscaleFields = document.getElementById('tailscaleFields');
    tailscaleFields.style.display = config.tailscale?.enabled ? 'block' : 'none';

    // Email
    document.getElementById('emailMethod').value = config.email?.method || 'disabled';
    document.getElementById('emailTo').value = config.email?.toEmail || '';
    document.getElementById('emailFrom').value = config.email?.fromEmail || 'security@webcam-system.local';

    // Show/hide email fields
    const emailFields = document.getElementById('emailFields');
    emailFields.style.display = config.email?.method !== 'disabled' ? 'block' : 'none';

    console.log('[Config] Form population complete');
    } catch (error) {
        console.error('[Config] Error populating form:', error);
        throw error; // Re-throw to be caught by loadConfig
    }
}

// Save configuration to API
async function saveConfig() {
    showAlert('', 'hide');

    // Validate required fields
    const saveDirectory = document.getElementById('saveDirectory').value.trim();
    if (!saveDirectory) {
        showAlert('Save Directory is required!', 'error');
        document.getElementById('saveDirectory').focus();
        return;
    }

    const webrtcPassword = document.getElementById('webrtcPassword').value;
    if (webrtcPassword && webrtcPassword.length < 8) {
        showAlert('WebRTC password must be at least 8 characters!', 'error');
        document.getElementById('webrtcPassword').focus();
        return;
    }

    // Collect form data
    const config = {
        recording: {
            continuousEnabled: document.getElementById('continuousRecording').checked,
            eventEnabled: document.getElementById('eventRecording').checked,
            saveDirectory: saveDirectory,
            segmentDuration: 5,
            preMotionBuffer: parseInt(document.getElementById('preMotionBuffer').value),
            postMotionBuffer: parseInt(document.getElementById('postMotionBuffer').value),
            retentionDays: 7
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
            hostname: '',
            listenPort: parseInt(document.getElementById('tailscalePort').value)
        },
        webrtc: {
            username: document.getElementById('webrtcUsername').value,
            password: webrtcPassword || undefined
        },
        email: {
            method: document.getElementById('emailMethod').value,
            toEmail: document.getElementById('emailTo').value,
            fromEmail: document.getElementById('emailFrom').value
        }
    };

    try {
        const response = await fetch(`${API_BASE_URL}/api/config`, {
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
        showAlert('✓ Configuration saved successfully! Restart the application for changes to take effect.', 'success');

        // Reload config to show saved values
        setTimeout(() => loadConfig(), 1000);

    } catch (error) {
        console.error('Error saving config:', error);
        showAlert(`Failed to save configuration: ${error.message}`, 'error');
    }
}

// Show/hide loading indicator
function showLoading(show) {
    document.getElementById('loading').style.display = show ? 'block' : 'none';
    document.getElementById('configForm').style.display = show ? 'none' : 'block';
}

// Show alert message
function showAlert(message, type) {
    const container = document.getElementById('alertContainer');
    if (type === 'hide' || !message) {
        container.innerHTML = '';
        return;
    }

    container.innerHTML = `
        <div class="alert ${type} show" style="margin: 20px 30px 0;">
            ${message}
        </div>
    `;

    // Auto-hide success messages after 5 seconds
    if (type === 'success') {
        setTimeout(() => showAlert('', 'hide'), 5000);
    }
}

// Generate random password in XXXXXX-XXXXXX format
function generatePassword(fieldId) {
    // Character set: uppercase, lowercase, numbers, and safe symbols
    const uppercase = 'ABCDEFGHJKLMNPQRSTUVWXYZ'; // Removed I, O for readability
    const lowercase = 'abcdefghjkmnpqrstuvwxyz'; // Removed i, l, o for readability
    const numbers = '23456789'; // Removed 0, 1 for readability
    const symbols = '!@#$%^&*';

    // Combine all characters
    const allChars = uppercase + lowercase + numbers + symbols;

    // Generate two groups of 6 characters each
    const group1 = generateRandomString(6, allChars);
    const group2 = generateRandomString(6, allChars);

    // Combine with hyphen
    const password = `${group1}-${group2}`;

    // Set the password in the field
    const field = document.getElementById(fieldId);
    if (field) {
        field.value = password;

        // Temporarily show the password
        const originalType = field.type;
        field.type = 'text';

        // Highlight the field briefly
        field.style.background = '#d4edda';

        // Revert after 3 seconds
        setTimeout(() => {
            field.type = originalType;
            field.style.background = '';
        }, 3000);

        console.log(`[Config] Generated password for ${fieldId}`);
    }
}

// Helper function to generate cryptographically random string
function generateRandomString(length, charset) {
    const randomValues = new Uint8Array(length);
    crypto.getRandomValues(randomValues);

    let result = '';
    for (let i = 0; i < length; i++) {
        // Use modulo to map random byte to charset index
        result += charset[randomValues[i] % charset.length];
    }

    return result;
}

// Toggle password visibility
function togglePasswordVisibility(fieldId) {
    const field = document.getElementById(fieldId);
    if (!field) {
        console.error(`[Config] Field not found: ${fieldId}`);
        return;
    }

    // Toggle between password and text
    if (field.type === 'password') {
        field.type = 'text';
        console.log(`[Config] Showing password for ${fieldId}`);
    } else {
        field.type = 'password';
        console.log(`[Config] Hiding password for ${fieldId}`);
    }
}
