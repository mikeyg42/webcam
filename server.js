const express = require('express');
const http = require('http');
const path = require('path');
const cors = require('cors');
const helmet = require('helmet');
const fs = require('fs');

// Rate-limited logger to prevent log flooding
const rateLimitedLog = (() => {
    const logHistory = new Map();
    const COOLDOWN_MS = 5000;
    const BATCH_REPORT_MS = 10000;

    setInterval(() => {
        const now = Date.now();
        logHistory.forEach((entry, key) => {
            if (entry.count > 1 && (now - entry.firstTime) > BATCH_REPORT_MS) {
                console.log(`[Log suppressed ${entry.count - 1}x in last ${Math.round((now - entry.firstTime) / 1000)}s]: ${key}`);
                logHistory.delete(key);
            }
        });
    }, BATCH_REPORT_MS);

    return {
        error: (key, ...args) => {
            const now = Date.now();
            const entry = logHistory.get(key);
            if (entry && (now - entry.lastTime) < COOLDOWN_MS) {
                entry.count++;
                entry.lastTime = now;
                return;
            }
            console.error(...args);
            logHistory.set(key, { count: 1, lastTime: now, firstTime: now });
        }
    };
})();

// Configuration
const config = {
    port: process.env.PORT || 3000,
    host: process.env.HOST || '0.0.0.0',
    cors: {
        origin: function(origin, callback) {
            // Allow requests with no origin (same-origin, curl, etc.)
            if (!origin) return callback(null, true);
            // Allow localhost on ports 3000-3010 (dynamic Node proxy range)
            // and 8081 (Go API) for development
            const localhostMatch = /^https?:\/\/(localhost|127\.0\.0\.1):(3\d{3}|8081)$/;
            // Allow Tailscale CGNAT range (100.64-127.x.x.x) on any port
            const tailscaleMatch = /^https?:\/\/100\.(6[4-9]|[7-9]\d|1[01]\d|12[0-7])\.\d{1,3}\.\d{1,3}(:\d+)?$/;
            if (localhostMatch.test(origin) || tailscaleMatch.test(origin)) {
                return callback(null, true);
            }
            callback(new Error('CORS not allowed'));
        },
        methods: ['GET', 'POST', 'PATCH', 'DELETE']
    }
};

// Initialize Express app
const app = express();

app.set('trust proxy', true);

// Security middleware
app.use(helmet({
    contentSecurityPolicy: {
        directives: {
            defaultSrc: ["'self'"],
            scriptSrc: ["'self'"],
            connectSrc: ["'self'", "ws:", "wss:"],
            mediaSrc: ["'self'", "blob:", "data:"],
            workerSrc: ["'self'", "blob:"],
            imgSrc: ["'self'", "data:", "blob:"],
            styleSrc: ["'self'", "'unsafe-inline'"],
            fontSrc: ["'self'"],
            objectSrc: ["'none'"],
            frameAncestors: ["'none'"],
            upgradeInsecureRequests: null
        }
    }
}));
app.use(cors(config.cors));
app.use(express.json({ limit: '1mb' }));

// Tailscale authentication middleware
function tailscaleAuthMiddleware(req, res, next) {
    if (process.env.TAILSCALE_ENABLED !== 'true') return next();

    const clientIP = req.ip || req.headers['x-forwarded-for'];
    const isTailscaleIP = (ip) => {
        if (ip && ip.startsWith('100.')) {
            const secondOctet = parseInt(ip.split('.')[1], 10);
            return secondOctet >= 64 && secondOctet <= 127;
        }
        if (ip && ip.startsWith('fd7a:115c:a1e0:')) return true;
        return ip === '127.0.0.1' || ip === '::1' || ip === 'localhost';
    };

    if (!isTailscaleIP(clientIP)) {
        console.warn(`Access denied for non-Tailscale IP: ${clientIP}`);
        return res.status(403).json({ error: 'Access denied', message: 'Only accessible via Tailscale' });
    }
    next();
}

if (process.env.TAILSCALE_ENABLED === 'true') {
    app.use(tailscaleAuthMiddleware);
}

// Serve static files from React build
const publicDir = path.join(__dirname, 'public-new');
const fallbackDir = path.join(__dirname, 'public');

if (fs.existsSync(publicDir)) {
    console.log('Serving React app from public-new/');
    app.use(express.static(publicDir));
} else {
    console.log('React build not found, serving from public/');
    app.use(express.static(fallbackDir));
}

// Create HTTP server
const server = http.createServer(app);

// Proxy all /api/* requests to Go backend
const goBackendUrl = process.env.GO_BACKEND_URL || 'http://localhost:8081';

function getProxyHeaders(req) {
    const clientIP = req.ip || req.socket.remoteAddress;
    return {
        'Content-Type': 'application/json',
        'X-Forwarded-For': clientIP,
        'X-Forwarded-Proto': req.protocol,
        'X-Forwarded-Host': req.get('host')
    };
}

app.use('/api', async (req, res) => {
    try {
        const url = `${goBackendUrl}/api${req.url}`;
        const controller = new AbortController();
        // 5 minutes for large downloads, 30s for everything else
        const isDownload = req.url.includes('/download') || req.url.includes('/stream');
        const timeoutMs = isDownload ? 300000 : 30000;
        const timeoutId = setTimeout(() => controller.abort(), timeoutMs);

        const options = {
            method: req.method,
            headers: getProxyHeaders(req),
            signal: controller.signal,
            redirect: 'manual',
        };

        if (req.method !== 'GET' && req.method !== 'HEAD') {
            options.body = JSON.stringify(req.body);
        }

        const response = await fetch(url, options);
        clearTimeout(timeoutId);

        // Pass through redirects (e.g., segment stream -> MinIO pre-signed URL)
        if (response.status >= 300 && response.status < 400) {
            const location = response.headers.get('location');
            if (location) {
                res.redirect(response.status, location);
                return;
            }
        }

        const contentType = response.headers.get('content-type') || '';

        // JSON responses — parse and forward
        if (contentType.includes('application/json')) {
            const data = await response.json();
            res.status(response.status).json(data);
            return;
        }

        // Binary responses (video, octet-stream) — pipe through with headers
        if (contentType.includes('video/') || contentType.includes('octet-stream') || contentType.includes('matroska')) {
            res.status(response.status);
            res.setHeader('Content-Type', contentType);
            const disposition = response.headers.get('content-disposition');
            if (disposition) res.setHeader('Content-Disposition', disposition);
            const contentLength = response.headers.get('content-length');
            if (contentLength) res.setHeader('Content-Length', contentLength);

            const { Readable } = require('stream');
            const nodeStream = Readable.fromWeb(response.body);
            nodeStream.pipe(res);
            return;
        }

        // Text/plain error responses from Go's http.Error()
        const text = await response.text();
        res.status(response.status).json({ error: text });
    } catch (error) {
        const errorMsg = error.name === 'AbortError' ? 'Request timed out' : error.message;
        rateLimitedLog.error(`api-proxy-${req.method}`, `Error proxying ${req.method} /api${req.url}:`, errorMsg);
        res.status(500).json({ error: `Failed to proxy request: ${errorMsg}` });
    }
});

// SPA catch-all — serve index.html for all non-API routes
app.get('*', (req, res) => {
    const indexPath = fs.existsSync(publicDir)
        ? path.join(publicDir, 'index.html')
        : path.join(fallbackDir, 'index.html');
    res.sendFile(indexPath);
});

// Error handling
app.use((err, req, res, next) => {
    console.error(err.stack);
    res.status(500).send('Something broke!');
});

// Start server
function startServer(port, maxAttempts = 10) {
    const tryPort = (currentPort, attempts) => {
        if (attempts > maxAttempts) {
            console.error(`Failed to find available port after ${maxAttempts} attempts`);
            process.exit(1);
        }

        server.listen(currentPort, config.host)
            .on('listening', () => {
                const actualPort = server.address().port;
                console.log(`Server running at http://${config.host}:${actualPort}`);
                console.log(`Proxying /api/* to Go backend at ${goBackendUrl}`);
            })
            .on('error', (err) => {
                if (err.code === 'EADDRINUSE') {
                    console.log(`Port ${currentPort} in use, trying ${currentPort + 1}...`);
                    tryPort(currentPort + 1, attempts + 1);
                } else {
                    console.error('Server error:', err);
                    process.exit(1);
                }
            });
    };
    tryPort(port, 1);
}

startServer(config.port);

// Graceful shutdown
process.on('SIGTERM', () => {
    console.log('SIGTERM received: closing server...');
    server.close(() => {
        console.log('Server closed');
        process.exit(0);
    });
    setTimeout(() => {
        console.error('Graceful shutdown timed out, forcing exit');
        process.exit(1);
    }, 10000);
});
