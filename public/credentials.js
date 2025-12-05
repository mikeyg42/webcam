// Credential management for auto-populated credentials

let userCredentialsStatus = {
    hasCredentials: false,
    userEmail: ''
};

// Check if user has credentials stored
async function checkCredentialsStatus() {
    try {
        const response = await fetch('/api/credentials/status');
        if (response.ok) {
            userCredentialsStatus = await response.json();
            return userCredentialsStatus.hasCredentials;
        } else {
            console.warn('Could not check credentials status:', response.statusText);
            return false;
        }
    } catch (error) {
        console.warn('Credential status check failed (Tailscale may not be enabled):', error);
        return false;
    }
}

// Show/hide credential fields based on status
function updateCredentialFieldsVisibility(hasCredentials) {
    const credentialSections = [
        'webrtc-credentials',
        'minio-credentials',
        'postgres-credentials'
    ];

    credentialSections.forEach(sectionId => {
        const section = document.getElementById(sectionId);
        if (section) {
            if (hasCredentials) {
                section.style.display = 'none';
                // Add info message
                if (!section.nextElementSibling || !section.nextElementSibling.classList.contains('credentials-info')) {
                    const info = document.createElement('div');
                    info.className = 'credentials-info';
                    info.style.cssText = 'padding: 12px; background: #d4edda; border: 1px solid #c3e6cb; border-radius: 6px; color: #155724; margin-bottom: 15px;';
                    info.innerHTML = `
                        <strong>✓ Credentials Loaded</strong><br>
                        <small>Your credentials are securely stored and auto-populated from encrypted storage.</small><br>
                        <button onclick="showUpdateCredentialsModal()" style="margin-top: 8px; padding: 6px 12px; background: #28a745; color: white; border: none; border-radius: 4px; cursor: pointer;">
                            Update Credentials
                        </button>
                    `;
                    section.parentNode.insertBefore(info, section);
                }
            } else {
                section.style.display = 'block';
                // Remove info message if it exists
                const info = section.previousElementSibling;
                if (info && info.classList.contains('credentials-info')) {
                    info.remove();
                }
            }
        }
    });

    // Show first-time setup message if no credentials
    if (!hasCredentials) {
        const header = document.querySelector('.header p');
        if (header && !header.textContent.includes('first-time')) {
            const setupMsg = document.createElement('div');
            setupMsg.className = 'first-time-setup';
            setupMsg.style.cssText = 'margin-top: 10px; padding: 10px; background: rgba(255,255,255,0.2); border-radius: 6px;';
            setupMsg.innerHTML = '<strong>First-time Setup:</strong> Please enter your credentials once. They\'ll be securely stored and auto-filled on future visits.';
            header.appendChild(setupMsg);
        }
    }
}

// Save credentials (first-time setup)
async function saveCredentials(credentials) {
    try {
        const response = await fetch('/api/credentials', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify(credentials)
        });

        if (response.ok) {
            const result = await response.json();
            console.log('Credentials saved:', result.message);
            return true;
        } else {
            const error = await response.text();
            console.error('Failed to save credentials:', error);
            return false;
        }
    } catch (error) {
        console.error('Error saving credentials:', error);
        return false;
    }
}

// Extract credentials from form
function extractCredentialsFromForm() {
    return {
        webrtc_username: document.getElementById('webrtcUsername')?.value || '',
        webrtc_password: document.getElementById('webrtcPassword')?.value || '',
        minio_access_key: document.getElementById('minioAccessKey')?.value || '',
        minio_secret_key: document.getElementById('minioSecretKey')?.value || '',
        postgres_username: document.getElementById('postgresUsername')?.value || '',
        postgres_password: document.getElementById('postgresPassword')?.value || ''
    };
}

// Show modal for updating credentials
function showUpdateCredentialsModal() {
    const modal = document.createElement('div');
    modal.id = 'credentialsModal';
    modal.style.cssText = 'position: fixed; top: 0; left: 0; right: 0; bottom: 0; background: rgba(0,0,0,0.5); display: flex; align-items: center; justify-content: center; z-index: 9999;';

    modal.innerHTML = `
        <div style="background: white; border-radius: 12px; padding: 30px; max-width: 600px; width: 90%; max-height: 80vh; overflow-y: auto;">
            <h2 style="margin-top: 0; color: #667eea;">Update Credentials</h2>
            <p style="color: #6c757d; margin-bottom: 20px;">Update your stored credentials. Leave a field empty to keep the existing value.</p>

            <div class="form-group">
                <label>WebRTC Username</label>
                <input type="text" id="modalWebrtcUsername" placeholder="Leave empty to keep current">
            </div>
            <div class="form-group">
                <label>WebRTC Password</label>
                <input type="password" id="modalWebrtcPassword" placeholder="Leave empty to keep current">
            </div>

            <div class="form-group">
                <label>MinIO Access Key</label>
                <input type="text" id="modalMinioAccessKey" placeholder="Leave empty to keep current">
            </div>
            <div class="form-group">
                <label>MinIO Secret Key</label>
                <input type="password" id="modalMinioSecretKey" placeholder="Leave empty to keep current">
            </div>

            <div class="form-group">
                <label>PostgreSQL Username</label>
                <input type="text" id="modalPostgresUsername" placeholder="Leave empty to keep current">
            </div>
            <div class="form-group">
                <label>PostgreSQL Password</label>
                <input type="password" id="modalPostgresPassword" placeholder="Leave empty to keep current">
            </div>

            <div style="display: flex; gap: 10px; justify-content: flex-end; margin-top: 20px;">
                <button onclick="closeCredentialsModal()" style="padding: 10px 20px; background: #6c757d; color: white; border: none; border-radius: 6px; cursor: pointer;">
                    Cancel
                </button>
                <button onclick="updateCredentials()" style="padding: 10px 20px; background: #667eea; color: white; border: none; border-radius: 6px; cursor: pointer;">
                    Save Changes
                </button>
            </div>
        </div>
    `;

    document.body.appendChild(modal);
}

function closeCredentialsModal() {
    const modal = document.getElementById('credentialsModal');
    if (modal) {
        modal.remove();
    }
}

async function updateCredentials() {
    const credentials = {
        webrtc_username: document.getElementById('modalWebrtcUsername').value,
        webrtc_password: document.getElementById('modalWebrtcPassword').value,
        minio_access_key: document.getElementById('modalMinioAccessKey').value,
        minio_secret_key: document.getElementById('modalMinioSecretKey').value,
        postgres_username: document.getElementById('modalPostgresUsername').value,
        postgres_password: document.getElementById('modalPostgresPassword').value
    };

    // Filter out empty values (keep existing)
    const filteredCreds = {};
    for (const [key, value] of Object.entries(credentials)) {
        if (value && value.trim() !== '') {
            filteredCreds[key] = value;
        }
    }

    if (Object.keys(filteredCreds).length === 0) {
        alert('Please enter at least one credential to update');
        return;
    }

    // Partial updates are now supported - only non-empty fields will be updated
    const success = await saveCredentials(filteredCreds);
    if (success) {
        alert('Credentials updated successfully!');
        closeCredentialsModal();
        window.location.reload(); // Reload to show updated state
    } else {
        alert('Failed to update credentials. Please try again.');
    }
}

// Initialize on page load
document.addEventListener('DOMContentLoaded', async function() {
    console.log('Checking credential status...');
    const hasCredentials = await checkCredentialsStatus();
    console.log('Has credentials:', hasCredentials);

    if (hasCredentials) {
        console.log('User:', userCredentialsStatus.userEmail);
    }

    updateCredentialFieldsVisibility(hasCredentials);

    // Hook into form submission to save credentials if first-time
    const configForm = document.getElementById('configForm');
    if (configForm && !hasCredentials) {
        const originalSubmit = configForm.onsubmit;
        configForm.onsubmit = async function(e) {
            // Extract and save credentials first
            const credentials = extractCredentialsFromForm();
            const saved = await saveCredentials(credentials);

            if (!saved) {
                console.warn('Could not save credentials, but continuing with config save');
            }

            // Continue with original form submission
            if (originalSubmit) {
                return originalSubmit.call(this, e);
            }
        };
    }
});
