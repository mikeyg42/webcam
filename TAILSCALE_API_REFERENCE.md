# Tailscale API v2 Reference Guide

This guide provides essential patterns and endpoints for reviewing Tailscale API integration code.

## Base Configuration

- **Base URL**: `https://api.tailscale.com/api/v2/`
- **Authentication**:
  - Bearer token: `Authorization: Bearer tskey-api-xxxxx`
  - Basic auth: username=`tskey-api-xxxxx`, password blank
  - OAuth tokens: `tskey-client-xxxxx` for OAuth client secrets
- **Content Type**: `application/json` for requests and responses

## Key Endpoints Used in This Codebase

### Device Management

1. **List Tailnet Devices**
   - `GET /tailnet/{tailnet}/devices`
   - OAuth Scope: `devices:core:read`
   - Supports filtering: `?<field>=<value>` (e.g., `?tags=tag:prod`)
   - Response: `{ "devices": [Device...] }`

2. **Get Device**
   - `GET /device/{deviceId}`
   - OAuth Scope: `devices:core:read`
   - Supports field selection via `?fields=` param

3. **Delete Device**
   - `DELETE /device/{deviceId}`
   - OAuth Scope: `devices:core`
   - Returns 200 on success, 501 if device not owned by tailnet

4. **Set Device Routes**
   - `POST /device/{deviceId}/routes`
   - OAuth Scope: `devices:routes`
   - Body: `{ "routes": ["10.0.0.0/16", "192.168.1.0/24"] }`
   - Routes must be both advertised AND enabled

5. **Set Device Tags**
   - `POST /device/{deviceId}/tags`
   - OAuth Scope: `devices:core`
   - Body: `{ "tags": ["tag:foo", "tag:bar"] }`

6. **Authorize Device**
   - `POST /device/{deviceId}/authorized`
   - OAuth Scope: `devices:core`
   - Body: `{ "authorized": true|false }`

7. **Update Device Key**
   - `POST /device/{deviceId}/key`
   - OAuth Scope: `devices:core`
   - Body: `{ "keyExpiryDisabled": bool }`

### Auth Keys

1. **List Tailnet Keys**
   - `GET /tailnet/{tailnet}/keys`
   - OAuth Scope: `auth_keys:read`

2. **Create Auth Key**
   - `POST /tailnet/{tailnet}/keys`
   - OAuth Scope: `auth_keys`
   - Complex schema - supports ephemeral, reusable, preauthorized, tags, etc.

3. **Delete Key**
   - `DELETE /tailnet/{tailnet}/keys/{keyId}`
   - OAuth Scope: `auth_keys`

## Common HTTP Status Codes

- `200` - Success
- `400` - Invalid request/parameters
- `404` - Resource not found (tailnet, device, etc.)
- `500` - Internal server error
- `501` - Not implemented / Operation not supported
- `504` - Request timeout

## Error Response Format

```json
{
  "message": "additional error information"
}
```

## Device Schema Key Fields

```typescript
{
  id: string                    // deviceId
  name: string                  // FQDN or base name
  hostname: string              // OS hostname
  addresses: string[]           // Tailscale IPs
  tags: string[]                // ACL tags (e.g., "tag:prod")
  authorized: boolean           // Authorization status
  keyExpiryDisabled: boolean    // Key expiry setting
  advertised_routes?: string[]  // Routes device advertises
  enabled_routes?: string[]     // Routes enabled for device
  isExternal?: boolean          // Shared from another tailnet
}
```

## OAuth Scopes Reference

- `devices:core:read` - Read device info
- `devices:core` - Full device management
- `devices:routes:read` - Read device routes
- `devices:routes` - Manage device routes
- `auth_keys:read` - Read auth keys
- `auth_keys` - Manage auth keys

## Best Practices

1. **Authentication**
   - Store tokens securely (env vars, secrets management)
   - Never log or expose tokens
   - Use OAuth for long-lived integrations

2. **Error Handling**
   - Always check status codes
   - Parse error messages from response body
   - Handle 504 timeouts with retry logic

3. **Device Management**
   - Use device IDs, not names (names can change)
   - Validate tailnet ownership before operations
   - Remember routes need both advertised + enabled

4. **Rate Limiting**
   - API doesn't document limits, but implement backoff
   - Batch operations where possible

5. **Field Selection**
   - Use `?fields=` parameter to reduce response size
   - Only request fields you need

## Common Integration Patterns

### Getting Device Info
```go
GET /device/{deviceId}?fields=name,addresses,tags
Authorization: Bearer tskey-api-xxxxx
```

### Enabling Subnet Routes
```go
POST /device/{deviceId}/routes
Authorization: Bearer tskey-api-xxxxx
Content-Type: application/json

{
  "routes": ["10.0.0.0/16"]
}
```

### Tagging a Device
```go
POST /device/{deviceId}/tags
Authorization: Bearer tskey-api-xxxxx
Content-Type: application/json

{
  "tags": ["tag:security-camera", "tag:prod"]
}
```

## Security Considerations

1. **Token Storage**: Never commit tokens to git
2. **Scope Principle**: Use minimum required OAuth scopes
3. **Validation**: Validate all inputs before API calls
4. **HTTPS Only**: Always use https:// for API calls
5. **Error Messages**: Don't expose sensitive info in user-facing errors

## Full API Documentation

For complete details, schemas, and all endpoints, search:
`/Users/mikeglendinning/projects/webcam2/tailscale-api.json`
