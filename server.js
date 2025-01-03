const express = require('express');
const http = require('http');
const WebSocket = require('ws');
const path = require('path');
const cors = require('cors');
const helmet = require('helmet');

// Configuration
const config = {
    port: process.env.PORT || 3000,
    host: process.env.HOST || 'localhost',
    cors: {
        origin: '*', // In production, specify exact origins
        methods: ['GET', 'POST']
    }
};

// Initialize Express app
const app = express();

// Security middleware
app.use(helmet()); // Adds various HTTP headers for security
app.use(cors(config.cors));
app.use(express.json()); // Parse JSON bodies

// Serve static files
app.use(express.static(path.join(__dirname, 'public')));

// Create HTTP server
const server = http.createServer(app);

// Create WebSocket server
const wss = new WebSocket.Server({ server });

// WebSocket connection handling
wss.on('connection', (ws, req) => {
    const clientIp = req.socket.remoteAddress;
    console.log(`New WebSocket connection from ${clientIp}`);

    // Handle incoming messages
    ws.on('message', (message) => {
        try {
            const data = JSON.parse(message);
            handleWebSocketMessage(ws, data);
        } catch (error) {
            console.error('Error processing message:', error);
            ws.send(JSON.stringify({
                type: 'error',
                message: 'Invalid message format'
            }));
        }
    });

    // Handle client disconnection
    ws.on('close', () => {
        console.log(`Client ${clientIp} disconnected`);
    });

    // Handle errors
    ws.on('error', (error) => {
        console.error(`WebSocket error for ${clientIp}:`, error);
    });
});

// WebSocket message handler
function handleWebSocketMessage(ws, data) {
    switch (data.type) {
        case 'offer':
        case 'answer':
        case 'ice-candidate':
            // Broadcast to all other clients
            wss.clients.forEach(client => {
                if (client !== ws && client.readyState === WebSocket.OPEN) {
                    client.send(JSON.stringify(data));
                }
            });
            break;
        default:
            console.warn('Unknown message type:', data.type);
    }
}

// Error handling middleware
app.use((err, req, res, next) => {
    console.error(err.stack);
    res.status(500).send('Something broke!');
});

// Start server
server.listen(config.port, config.host, () => {
    console.log(`Server running at http://${config.host}:${config.port}`);
});

// Graceful shutdown
process.on('SIGTERM', () => {
    console.log('SIGTERM signal received: closing HTTP server');
    server.close(() => {
        console.log('HTTP server closed');
        process.exit(0);
    });
});

// Handle uncaught exceptions
process.on('uncaughtException', (err) => {
    console.error('Uncaught Exception:', err);
    process.exit(1);
});

// Handle unhandled promise rejections
process.on('unhandledRejection', (reason, promise) => {
    console.error('Unhandled Rejection at:', promise, 'reason:', reason);
    process.exit(1);
});

module.exports = server; // Export for testing