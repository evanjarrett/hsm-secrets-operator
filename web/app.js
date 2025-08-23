class HSMSecretsAPI {
    constructor(baseUrl = '') {
        this.baseUrl = baseUrl;
        this.apiPath = '/api/v1';
    }

    async request(path, options = {}) {
        const url = `${this.baseUrl}${this.apiPath}${path}`;
        const config = {
            headers: {
                'Content-Type': 'application/json',
                ...options.headers
            },
            ...options
        };

        try {
            const response = await fetch(url, config);
            const data = await response.json();
            
            if (!response.ok) {
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

    async createSecret(secretName, data) {
        return this.request(`/hsm/secrets/${encodeURIComponent(secretName)}`, {
            method: 'POST',
            body: JSON.stringify({ data })
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
        this.setupEventListeners();
        this.loadInitialData();
        this.initializeCreateForm();
    }

    initializeCreateForm() {
        // Add initial empty key-value pair to the form
        this.addKeyValuePair();
    }

    setupEventListeners() {
        const createForm = document.getElementById('createForm');
        createForm.addEventListener('submit', (e) => this.handleCreateSecret(e));
        
        // Auto-refresh every 30 seconds
        setInterval(() => this.refreshSecrets(), 30000);
    }

    async loadInitialData() {
        await this.checkAPIHealth();
        await this.loadSecrets();
    }

    async checkAPIHealth() {
        try {
            const health = await this.api.getHealth();
            const statusElement = document.getElementById('apiStatus');
            
            if (health.success && health.data.status === 'healthy') {
                statusElement.textContent = '✅ Healthy';
                statusElement.style.color = '#22543d';
            } else {
                statusElement.textContent = '⚠️ Degraded';
                statusElement.style.color = '#dd6b20';
            }
        } catch (error) {
            const statusElement = document.getElementById('apiStatus');
            statusElement.textContent = '❌ Error';
            statusElement.style.color = '#c53030';
            console.error('Health check failed:', error);
        }
    }

    async loadSecrets() {
        const listElement = document.getElementById('secretsList');
        listElement.innerHTML = '<div class="loading">Loading secrets...</div>';

        try {
            const response = await this.api.listSecrets();
            this.secrets = response.data.paths || [];
            
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
            
            detailsElement.innerHTML = `
                <h3>Secret: ${this.escapeHtml(secretName)}</h3>
                <div style="margin: 15px 0;">
                    <strong>Path:</strong> ${this.escapeHtml(secretData.path || secretName)}<br>
                    <strong>Checksum:</strong> ${this.escapeHtml(secretData.checksum || 'N/A')}<br>
                    <strong>Size:</strong> ${secretData.data ? Object.keys(secretData.data).length : 0} keys
                </div>
                <div>
                    <strong>Data:</strong>
                    <div class="json-preview">${this.escapeHtml(JSON.stringify(secretData.data || {}, null, 2))}</div>
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
            this.showSuccess(`Secret "${secretName}" deleted successfully!`);
            await this.loadSecrets();
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

        // Validate key names (no spaces, no special chars except underscore)
        for (const key of Object.keys(secretData)) {
            if (!/^[a-zA-Z][a-zA-Z0-9_]*$/.test(key)) {
                this.showError(messageElement, `Invalid key "${key}". Keys must start with a letter and contain only letters, numbers, and underscores.`);
                return;
            }
        }

        try {
            // Show loading state
            const submitBtn = event.target.querySelector('button[type="submit"]');
            const originalText = submitBtn.textContent;
            submitBtn.textContent = 'Creating...';
            submitBtn.disabled = true;

            await this.api.createSecret(secretName, secretData);
            
            this.showSuccess(messageElement, `Secret "${secretName}" created successfully!`);
            
            // Reset form and refresh list
            event.target.reset();
            await this.loadSecrets();
            
            // Hide form after a delay
            setTimeout(() => this.hideCreateForm(), 2000);
            
        } catch (error) {
            this.showError(messageElement, `Failed to create secret: ${error.message}`);
        } finally {
            // Restore button state
            const submitBtn = event.target.querySelector('button[type="submit"]');
            submitBtn.textContent = 'Create Secret';
            submitBtn.disabled = false;
        }
    }

    async refreshSecrets() {
        await this.loadSecrets();
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
});

// Expose functions globally for onclick handlers
window.refreshSecrets = () => ui.refreshSecrets();
window.showCreateForm = () => ui.showCreateForm();
window.hideCreateForm = () => ui.hideCreateForm();
window.hideViewSection = () => ui.hideViewSection();