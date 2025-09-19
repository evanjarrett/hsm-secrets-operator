class HSMTokenManager {
    constructor(baseUrl = '') {
        this.baseUrl = baseUrl;
        this.apiPath = '/api/v1';
        this.storageKey = 'hsm-token';
        this.expiryKey = 'hsm-token-expiry';
        this.cachedToken = null;
        this.tokenExpiry = null;
        this.refreshPromise = null;
        this.loadCachedToken();
    }

    loadCachedToken() {
        const token = localStorage.getItem(this.storageKey);
        const expiry = localStorage.getItem(this.expiryKey);

        if (token && expiry) {
            this.cachedToken = token;
            this.tokenExpiry = new Date(expiry);
        }
    }

    isTokenValid() {
        if (!this.cachedToken || !this.tokenExpiry) {
            return false;
        }

        // Consider token invalid if it expires within 5 minutes
        const bufferTime = 5 * 60 * 1000; // 5 minutes in milliseconds
        return new Date() < (new Date(this.tokenExpiry.getTime() - bufferTime));
    }

    async getValidToken() {
        // Return cached token if still valid
        if (this.isTokenValid()) {
            return this.cachedToken;
        }

        // If already refreshing, wait for that promise
        if (this.refreshPromise) {
            return await this.refreshPromise;
        }

        // Start token refresh
        this.refreshPromise = this.refreshToken();

        try {
            const token = await this.refreshPromise;
            return token;
        } finally {
            this.refreshPromise = null;
        }
    }

    async refreshToken() {
        try {
            // First try to get a K8s token automatically (if kubectl is configured)
            let k8sToken = await this.getK8sToken();

            if (!k8sToken) {
                // Prompt user for token
                k8sToken = await this.promptForK8sToken();
            }

            // Exchange K8s token for HSM JWT
            const response = await fetch(`${this.baseUrl}${this.apiPath}/auth/token`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify({ k8s_token: k8sToken })
            });

            if (!response.ok) {
                const errorData = await response.json();
                throw new Error(errorData.error || `HTTP ${response.status}`);
            }

            const data = await response.json();

            // Cache the new token
            this.cachedToken = data.token;
            this.tokenExpiry = new Date(data.expires_at);

            localStorage.setItem(this.storageKey, this.cachedToken);
            localStorage.setItem(this.expiryKey, this.tokenExpiry.toISOString());

            return this.cachedToken;
        } catch (error) {
            console.error('Token refresh failed:', error);
            this.clearToken();
            throw error;
        }
    }

    async getK8sToken() {
        // This would work if the web UI had access to kubectl context
        // For now, we'll return null to trigger user prompt
        return null;
    }

    async promptForK8sToken() {
        return new Promise((resolve, reject) => {
            // Create modal dialog
            const modal = this.createTokenModal();
            document.body.appendChild(modal);

            // Focus on input
            const input = modal.querySelector('#tokenInput');
            const submitBtn = modal.querySelector('#submitToken');
            const cancelBtn = modal.querySelector('#cancelToken');

            input.focus();

            const cleanup = () => {
                document.body.removeChild(modal);
            };

            submitBtn.onclick = () => {
                const token = input.value.trim();
                if (token) {
                    cleanup();
                    resolve(token);
                } else {
                    alert('Please enter a valid token');
                }
            };

            cancelBtn.onclick = () => {
                cleanup();
                reject(new Error('Authentication cancelled by user'));
            };

            // Submit on Enter
            input.onkeydown = (e) => {
                if (e.key === 'Enter') {
                    submitBtn.click();
                }
            };
        });
    }

    createTokenModal() {
        const modal = document.createElement('div');
        modal.className = 'auth-modal';
        modal.innerHTML = `
            <div class="auth-modal-content">
                <h2>🔐 Authentication Required</h2>
                <p>The HSM Secrets API requires authentication. Please provide a Kubernetes service account token.</p>

                <div class="auth-instructions">
                    <p><strong>To get a token, run this command:</strong></p>
                    <code>kubectl create token hsm-web-ui-sa --duration=8h</code>
                    <p><small>Replace <code>hsm-web-ui-sa</code> with your service account name</small></p>
                </div>

                <div class="form-group">
                    <label for="tokenInput">Service Account Token:</label>
                    <textarea id="tokenInput" placeholder="Paste your Kubernetes service account token here..." rows="4"></textarea>
                </div>

                <div class="auth-actions">
                    <button id="submitToken" class="btn">Login</button>
                    <button id="cancelToken" class="btn btn-secondary">Cancel</button>
                </div>
            </div>
        `;
        return modal;
    }

    clearToken() {
        this.cachedToken = null;
        this.tokenExpiry = null;
        localStorage.removeItem(this.storageKey);
        localStorage.removeItem(this.expiryKey);
    }

    getTokenInfo() {
        if (!this.cachedToken || !this.tokenExpiry) {
            return { authenticated: false };
        }

        return {
            authenticated: true,
            expiresAt: this.tokenExpiry,
            valid: this.isTokenValid()
        };
    }
}

class HSMSecretsAPI {
    constructor(baseUrl = '') {
        this.baseUrl = baseUrl;
        this.apiPath = '/api/v1';
        this.tokenManager = new HSMTokenManager(baseUrl);
    }

    async request(path, options = {}) {
        const url = `${this.baseUrl}${this.apiPath}${path}`;

        // Skip authentication for health and auth endpoints
        const skipAuth = path.includes('/health') || path.includes('/auth/token');

        const headers = {
            'Content-Type': 'application/json',
            ...options.headers
        };

        // Add authentication header if not skipping auth
        if (!skipAuth) {
            try {
                const token = await this.tokenManager.getValidToken();
                headers['Authorization'] = `Bearer ${token}`;
            } catch (error) {
                throw new Error(`Authentication failed: ${error.message}`);
            }
        }

        const config = {
            headers,
            ...options
        };

        try {
            const response = await fetch(url, config);
            const data = await response.json();

            if (!response.ok) {
                // Handle authentication errors specifically
                if (response.status === 401) {
                    this.tokenManager.clearToken();
                    throw new Error('Authentication failed. Please login again.');
                }
                throw new Error(data.error?.message || `HTTP ${response.status}`);
            }

            return data;
        } catch (error) {
            console.error('API Request failed:', error);
            throw error;
        }
    }

    async getHealth() {
        return this.request('/health');
    }

    async listSecrets(page = 1, pageSize = 100) {
        return this.request(`/hsm/secrets?page=${page}&page_size=${pageSize}`);
    }

    async getSecret(secretName) {
        return this.request(`/hsm/secrets/${encodeURIComponent(secretName)}`);
    }

    async getDeviceStatus() {
        return this.request('/hsm/status');
    }

    async getDeviceInfo() {
        return this.request('/hsm/info');
    }

    async createSecret(secretName, data, metadata = null) {
        const requestBody = { data };
        if (metadata) {
            requestBody.metadata = metadata;
        }
        return this.request(`/hsm/secrets/${encodeURIComponent(secretName)}`, {
            method: 'POST',
            body: JSON.stringify(requestBody)
        });
    }

    async deleteSecret(secretName) {
        return this.request(`/hsm/secrets/${encodeURIComponent(secretName)}`, {
            method: 'DELETE'
        });
    }
}

class HSMSecretsUI {
    constructor() {
        this.api = new HSMSecretsAPI();
        this.secrets = [];
        this.init();
    }

    init() {
        this.kvPairCounter = 0;
        this.labelPairCounter = 0;
        this.setupEventListeners();
        this.loadInitialData();
        this.initializeCreateForm();
        this.updateAuthStatus();

        // Update auth status every 30 seconds
        setInterval(() => this.updateAuthStatus(), 30000);
    }

    initializeCreateForm() {
        // Add initial empty key-value pair to the form
        this.addKeyValuePair();
        // Add initial empty label pair to the metadata form
        this.addLabelPair();
    }

    setupEventListeners() {
        const createForm = document.getElementById('createForm');
        createForm.addEventListener('submit', (e) => this.handleCreateSecret(e));
    }

    async loadInitialData() {
        await this.checkAPIHealth();
        await this.loadDeviceStatus();
        await this.loadSecrets();
    }

    async checkAPIHealth() {
        try {
            const health = await this.api.getHealth();
            const statusElement = document.getElementById('apiStatus');
            const deviceCountElement = document.getElementById('deviceCount');
            
            if (health.success && health.data.status === 'healthy') {
                statusElement.textContent = '✅ Healthy';
                statusElement.style.color = '#22543d';
            } else {
                statusElement.textContent = '⚠️ Degraded';
                statusElement.style.color = '#dd6b20';
            }

            // Update device count if available
            if (deviceCountElement && health.data.activeNodes !== undefined) {
                deviceCountElement.textContent = health.data.activeNodes;
            }
        } catch (error) {
            const statusElement = document.getElementById('apiStatus');
            statusElement.textContent = '❌ Error';
            statusElement.style.color = '#c53030';
            console.error('Health check failed:', error);
        }
    }

    async loadDeviceStatus() {
        const statusElement = document.getElementById('deviceStatus');
        statusElement.innerHTML = '<div class="loading">Loading device status...</div>';

        try {
            const [statusResponse, infoResponse] = await Promise.all([
                this.api.getDeviceStatus(),
                this.api.getDeviceInfo()
            ]);

            const devices = statusResponse.data.devices || {};
            const deviceInfos = infoResponse.data.deviceInfos || {};
            const totalDevices = statusResponse.data.totalDevices || 0;

            this.renderDeviceStatus(devices, deviceInfos, totalDevices);
        } catch (error) {
            this.showError(statusElement, `Failed to load device status: ${error.message}`);
        }
    }

    renderDeviceStatus(devices, deviceInfos, totalDevices) {
        const statusElement = document.getElementById('deviceStatus');
        
        if (totalDevices === 0) {
            statusElement.innerHTML = '<p style="text-align: center; color: #666; padding: 20px;">No HSM devices found.</p>';
            return;
        }

        const deviceItems = Object.entries(devices).map(([deviceName, isConnected]) => {
            const info = deviceInfos[deviceName];
            const statusIcon = isConnected ? '🟢' : '🔴';
            const statusText = isConnected ? 'Connected' : 'Disconnected';
            
            return `
                <div class="device-item ${isConnected ? 'connected' : 'disconnected'}">
                    <div class="device-header">
                        <span class="device-name">${statusIcon} ${this.escapeHtml(deviceName)}</span>
                        <span class="device-status-badge">${statusText}</span>
                    </div>
                    ${info ? `
                        <div class="device-details">
                            <div class="device-info">
                                <span>Manufacturer: ${this.escapeHtml(info.manufacturer || 'Unknown')}</span>
                                <span>Model: ${this.escapeHtml(info.model || 'Unknown')}</span>
                                <span>Serial: ${this.escapeHtml(info.serialNumber || 'Unknown')}</span>
                            </div>
                        </div>
                    ` : ''}
                </div>
            `;
        }).join('');

        statusElement.innerHTML = deviceItems;
    }

    async loadSecrets() {
        const listElement = document.getElementById('secretsList');
        listElement.innerHTML = '<div class="loading">Loading secrets...</div>';

        try {
            const response = await this.api.listSecrets();
            this.secrets = response.data.secrets || [];
            
            document.getElementById('totalSecrets').textContent = this.secrets.length;
            
            this.renderSecretsList();
        } catch (error) {
            this.showError(listElement, `Failed to load secrets: ${error.message}`);
        }
    }

    renderSecretsList() {
        const listElement = document.getElementById('secretsList');
        
        if (this.secrets.length === 0) {
            listElement.innerHTML = '<p style="text-align: center; color: #666; padding: 20px;">No secrets found. Create your first secret!</p>';
            return;
        }

        listElement.innerHTML = this.secrets.map(secretName => `
            <div class="secret-item">
                <div class="secret-name">🔐 ${this.escapeHtml(secretName)}</div>
                <div class="secret-actions">
                    <button class="btn btn-secondary" onclick="ui.viewSecret('${this.escapeHtml(secretName)}')">
                        👁️ View
                    </button>
                    <button class="btn btn-danger" onclick="ui.deleteSecret('${this.escapeHtml(secretName)}')">
                        🗑️ Delete
                    </button>
                </div>
            </div>
        `).join('');
    }

    async viewSecret(secretName) {
        const viewSection = document.getElementById('viewSection');
        const viewMessage = document.getElementById('viewMessage');
        const detailsElement = document.getElementById('secretDetails');
        
        viewSection.style.display = 'block';
        viewMessage.innerHTML = '';
        detailsElement.innerHTML = '<div class="loading">Loading secret details...</div>';
        
        // Scroll to view section
        viewSection.scrollIntoView({ behavior: 'smooth' });

        try {
            const response = await this.api.getSecret(secretName);
            const secretData = response.data;
            
            // Convert byte arrays to strings for display
            const displayData = {};
            if (secretData.data) {
                for (const [key, value] of Object.entries(secretData.data)) {
                    // Handle byte arrays by converting to string
                    if (Array.isArray(value)) {
                        displayData[key] = String.fromCharCode.apply(null, value);
                    } else {
                        displayData[key] = value;
                    }
                }
            }

            const deviceBadge = secretData.deviceCount > 1 ? 
                `<span class="device-badge multi-device">${secretData.deviceCount} devices</span>` :
                `<span class="device-badge single-device">1 device</span>`;
            
            detailsElement.innerHTML = `
                <h3>Secret: ${this.escapeHtml(secretName)} ${deviceBadge}</h3>
                <div class="secret-metadata">
                    <div class="metadata-item">
                        <strong>Path:</strong> ${this.escapeHtml(secretData.path || secretName)}
                    </div>
                    <div class="metadata-item">
                        <strong>Checksum:</strong> ${this.escapeHtml(secretData.checksum || 'N/A')}
                    </div>
                    <div class="metadata-item">
                        <strong>Keys:</strong> ${Object.keys(displayData).length}
                    </div>
                    ${secretData.deviceCount ? `
                        <div class="metadata-item">
                            <strong>Device Count:</strong> ${secretData.deviceCount}
                        </div>
                    ` : ''}
                </div>
                <div class="secret-data">
                    <strong>Data:</strong>
                    <div class="json-preview">${this.escapeHtml(JSON.stringify(displayData, null, 2))}</div>
                </div>
            `;
        } catch (error) {
            this.showError(viewMessage, `Failed to load secret: ${error.message}`);
            detailsElement.innerHTML = '';
        }
    }

    async deleteSecret(secretName) {
        if (!confirm(`Are you sure you want to delete the secret "${secretName}"? This action cannot be undone.`)) {
            return;
        }

        try {
            await this.api.deleteSecret(secretName);
            this.showSuccess(null, `Secret "${secretName}" deleted successfully!`);
            await this.loadSecrets(); // Refresh after deletion
        } catch (error) {
            this.showError(null, `Failed to delete secret: ${error.message}`);
        }
    }

    showCreateForm() {
        document.getElementById('createSection').style.display = 'block';
        document.getElementById('secretName').focus();
        document.getElementById('createSection').scrollIntoView({ behavior: 'smooth' });
    }

    hideCreateForm() {
        document.getElementById('createSection').style.display = 'none';
        document.getElementById('createForm').reset();
        document.getElementById('createMessage').innerHTML = '';
        
        // Reset key-value pairs to single empty pair
        const kvPairs = document.getElementById('kvPairs');
        kvPairs.innerHTML = '';
        this.kvPairCounter = 0;
        this.addKeyValuePair(); // Add one empty pair
        
        // Reset label pairs and advanced section
        const labelPairs = document.getElementById('labelPairs');
        labelPairs.innerHTML = '';
        this.labelPairCounter = 0;
        this.addLabelPair(); // Add one empty label pair
        
        // Close advanced section
        const advancedContent = document.getElementById('advancedContent');
        const advancedToggle = document.querySelector('.advanced-toggle');
        advancedContent.classList.remove('show');
        advancedToggle.classList.remove('expanded');
    }

    hideViewSection() {
        document.getElementById('viewSection').style.display = 'none';
        document.getElementById('viewMessage').innerHTML = '';
    }

    addKeyValuePair(key = '', value = '') {
        const kvPairs = document.getElementById('kvPairs');
        
        const pairId = this.kvPairCounter++;
        const pairDiv = document.createElement('div');
        pairDiv.className = 'kv-pair';
        pairDiv.id = `kvPair${pairId}`;
        
        pairDiv.innerHTML = `
            <input type="text" name="key${pairId}" placeholder="Key (e.g., api_key)" value="${this.escapeHtml(key)}" required>
            <input type="text" name="value${pairId}" placeholder="Value" value="${this.escapeHtml(value)}" required>
            <button type="button" class="btn btn-remove btn-small" onclick="ui.removeKeyValuePair('kvPair${pairId}')" title="Remove this key-value pair">
                ➖
            </button>
        `;
        
        kvPairs.appendChild(pairDiv);
        
        // Focus on the key input for new pairs (but not during initial load)
        if (!key && kvPairs.children.length > 1) {
            pairDiv.querySelector('input[name^="key"]').focus();
        }
    }

    removeKeyValuePair(pairId) {
        const kvPairs = document.getElementById('kvPairs');
        const pairElement = document.getElementById(pairId);
        
        // Don't allow removing the last pair
        if (kvPairs.children.length <= 1) {
            return;
        }
        
        if (pairElement) {
            pairElement.remove();
        }
    }

    collectKeyValuePairs() {
        const kvPairs = document.getElementById('kvPairs');
        const pairs = kvPairs.querySelectorAll('.kv-pair');
        const data = {};
        
        for (const pair of pairs) {
            const keyInput = pair.querySelector('input[name^="key"]');
            const valueInput = pair.querySelector('input[name^="value"]');
            
            if (keyInput && valueInput) {
                const key = keyInput.value.trim();
                const value = valueInput.value.trim();
                
                if (key && value) {
                    data[key] = value;
                }
            }
        }
        
        return data;
    }

    toggleAdvanced() {
        const content = document.getElementById('advancedContent');
        const toggle = document.querySelector('.advanced-toggle');
        
        content.classList.toggle('show');
        toggle.classList.toggle('expanded');
    }

    addLabelPair(key = '', value = '') {
        const labelPairs = document.getElementById('labelPairs');
        
        const pairId = this.labelPairCounter++;
        const pairDiv = document.createElement('div');
        pairDiv.className = 'tag-pair';
        pairDiv.id = `labelPair${pairId}`;
        
        pairDiv.innerHTML = `
            <input type="text" name="labelKey${pairId}" placeholder="Label key (e.g., app, environment)" value="${this.escapeHtml(key)}">
            <input type="text" name="labelValue${pairId}" placeholder="Label value (e.g., backend, production)" value="${this.escapeHtml(value)}">
            <button type="button" class="btn btn-remove btn-small" onclick="ui.removeLabelPair('labelPair${pairId}')" title="Remove this label">
                ➖
            </button>
        `;
        
        labelPairs.appendChild(pairDiv);
        
        // Focus on the key input for new pairs (but not during initial load)
        if (!key && labelPairs.children.length > 1) {
            pairDiv.querySelector('input[name^="labelKey"]').focus();
        }
    }

    removeLabelPair(pairId) {
        const labelPairs = document.getElementById('labelPairs');
        const pairElement = document.getElementById(pairId);
        
        // Don't allow removing the last pair
        if (labelPairs.children.length <= 1) {
            return;
        }
        
        if (pairElement) {
            pairElement.remove();
        }
    }

    collectLabelPairs() {
        const labelPairs = document.getElementById('labelPairs');
        const pairs = labelPairs.querySelectorAll('.tag-pair');
        const labels = {};
        
        for (const pair of pairs) {
            const keyInput = pair.querySelector('input[name^="labelKey"]');
            const valueInput = pair.querySelector('input[name^="labelValue"]');
            
            if (keyInput && valueInput) {
                const key = keyInput.value.trim();
                const value = valueInput.value.trim();
                
                if (key && value) {
                    labels[key] = value;
                }
            }
        }
        
        return labels;
    }

    collectMetadata() {
        const description = document.getElementById('metadataDescription').value.trim();
        const format = document.getElementById('metadataFormat').value.trim();
        const dataType = document.getElementById('metadataDataType').value.trim();
        const source = document.getElementById('metadataSource').value.trim();
        const labels = this.collectLabelPairs();
        
        // Only return metadata if at least one field is filled
        if (!description && !format && !dataType && !source && Object.keys(labels).length === 0) {
            return null;
        }
        
        const metadata = {};
        if (description) metadata.description = description;
        if (format) metadata.format = format;
        if (dataType) metadata.data_type = dataType;
        if (source) metadata.source = source;
        if (Object.keys(labels).length > 0) metadata.labels = labels;
        
        // Add creation timestamp
        metadata.created_at = new Date().toISOString();
        
        return metadata;
    }

    async handleCreateSecret(event) {
        event.preventDefault();
        
        const messageElement = document.getElementById('createMessage');
        const formData = new FormData(event.target);
        const secretName = formData.get('secretName').trim();
        
        messageElement.innerHTML = '';

        // Validate inputs
        if (!secretName) {
            this.showError(messageElement, 'Secret name is required');
            return;
        }

        // Collect key-value pairs
        const secretData = this.collectKeyValuePairs();
        
        if (Object.keys(secretData).length === 0) {
            this.showError(messageElement, 'At least one key-value pair is required');
            return;
        }
        
        // Collect metadata if any is provided
        const metadata = this.collectMetadata();

        // Validate key names (no spaces, no special chars except underscore)
        for (const key of Object.keys(secretData)) {
            if (!/^[a-zA-Z][a-zA-Z0-9_]*$/.test(key)) {
                this.showError(messageElement, `Invalid key "${key}". Keys must start with a letter and contain only letters, numbers, and underscores.`);
                return;
            }
        }

        // Get submit button and store original text
        const submitBtn = event.target.querySelector('button[type="submit"]');
        const originalText = submitBtn.textContent;

        try {
            // Show loading state
            submitBtn.textContent = 'Creating...';
            submitBtn.disabled = true;

            await this.api.createSecret(secretName, secretData, metadata);
            
            this.showSuccess(messageElement, `Secret "${secretName}" created successfully!`);
            
            // Reset form and refresh list
            event.target.reset();
            await this.loadSecrets(); // Refresh after creation
            
            // Hide form after a delay
            setTimeout(() => this.hideCreateForm(), 2000);
            
        } catch (error) {
            this.showError(messageElement, `Failed to create secret: ${error.message}`);
        } finally {
            // Restore button state
            submitBtn.textContent = originalText;
            submitBtn.disabled = false;
        }
    }

    async refreshSecrets() {
        await this.loadSecrets();
    }

    async refreshDeviceStatus() {
        await this.loadDeviceStatus();
    }

    async refreshAll() {
        await this.loadInitialData();
        this.updateAuthStatus();
    }

    updateAuthStatus() {
        const authInfo = this.api.tokenManager.getTokenInfo();
        const authElement = document.getElementById('authStatus');

        if (authElement) {
            if (authInfo.authenticated && authInfo.valid) {
                const expiresIn = Math.floor((authInfo.expiresAt - new Date()) / (1000 * 60)); // minutes
                authElement.innerHTML = `✅ Authenticated (expires in ${expiresIn}m)`;
                authElement.className = 'auth-status authenticated';
            } else if (authInfo.authenticated && !authInfo.valid) {
                authElement.innerHTML = `⚠️ Token Expired`;
                authElement.className = 'auth-status expired';
            } else {
                authElement.innerHTML = `❌ Not Authenticated`;
                authElement.className = 'auth-status not-authenticated';
            }
        }
    }

    logout() {
        if (confirm('Are you sure you want to logout? You will need to provide a new token to continue using the HSM API.')) {
            this.api.tokenManager.clearToken();
            this.updateAuthStatus();
            this.showSuccess(null, 'Logged out successfully. You will be prompted for authentication on your next API request.');
        }
    }

    showError(element, message) {
        const errorHTML = `<div class="error">❌ ${this.escapeHtml(message)}</div>`;
        if (element) {
            element.innerHTML = errorHTML;
        } else {
            // Show at top of page
            const container = document.querySelector('.container');
            const existingError = container.querySelector('.error');
            if (existingError) {
                existingError.remove();
            }
            container.insertAdjacentHTML('afterbegin', errorHTML);
            
            // Remove after 5 seconds
            setTimeout(() => {
                const errorEl = container.querySelector('.error');
                if (errorEl) errorEl.remove();
            }, 5000);
        }
    }

    showSuccess(element, message) {
        const successHTML = `<div class="success">✅ ${this.escapeHtml(message)}</div>`;
        if (element) {
            element.innerHTML = successHTML;
        } else {
            // Show at top of page
            const container = document.querySelector('.container');
            const existingSuccess = container.querySelector('.success');
            if (existingSuccess) {
                existingSuccess.remove();
            }
            container.insertAdjacentHTML('afterbegin', successHTML);
            
            // Remove after 5 seconds
            setTimeout(() => {
                const successEl = container.querySelector('.success');
                if (successEl) successEl.remove();
            }, 5000);
        }
    }

    escapeHtml(unsafe) {
        return unsafe
            .replace(/&/g, "&amp;")
            .replace(/</g, "&lt;")
            .replace(/>/g, "&gt;")
            .replace(/"/g, "&quot;")
            .replace(/'/g, "&#039;");
    }
}

// Global functions for onclick handlers
let ui;

window.addEventListener('DOMContentLoaded', () => {
    ui = new HSMSecretsUI();
    // Expose ui object globally for onclick handlers
    window.ui = ui;
});

// Expose functions globally for onclick handlers
window.refreshSecrets = () => ui.refreshSecrets();
window.refreshDeviceStatus = () => ui.refreshDeviceStatus();
window.refreshAll = () => ui.refreshAll();
window.showCreateForm = () => ui.showCreateForm();
window.hideCreateForm = () => ui.hideCreateForm();
window.hideViewSection = () => ui.hideViewSection();
window.logout = () => ui.logout();

