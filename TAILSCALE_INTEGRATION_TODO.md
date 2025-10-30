# Tailscale-Only Integration Plan

> **Goal**: Remove TURN entirely and force all signaling + media over Tailscale. Actually use your Tailscale IP (tm.localIP) to control ICE candidate gathering.

## Updated Review (Focused on New Files)

* **Node proxy (server-tailscale.js)**
  - ✅ Good: Auto-discovers Tailscale peer and rewrites Ion SFU URL to tailnet IP (`getOptimizedIonSfuUrl`)
  - ✅ Good: Captures Tailscale status/peers periodically
  - ⚠️ Gap: FE still falls back to "traditional" networking

* **Front-end (index-tailscale.js)**
  - ⚠️ Explicitly falls back to "traditional" config
  - ⚠️ Leaves TURN as commented suggestion in config path
  - ❌ For Tailscale-only: fallback should be removed or turned into "connect Tailscale" error

* **SFU config (sfu.toml)**
  - ❌ TURN is enabled
  - ❌ TURN URL baked into iceserver list
  - This contradicts "no TURN"

* **Connection health (connectionHealth.go)**
  - ❌ Measures and warns about TURN health/allocations
  - Remove in Tailscale-only system

* **TURN server (stun_server.go)**
  - ❌ Full TURN server implementation (create/start/stop)
  - ❌ Realm/relay plumbing
  - Delete if committing to Tailscale-only

* **Manager lifecycle (manager.go)**
  - ❌ Still manages `turnServer` on shutdown
  - Another hint TURN is intertwined

---

## Principles (Enforce Everywhere)

* ❌ No `tailscale up` inside Go app. Provision Tailscale externally (install + `tailscale up`) and let app **query status only**
* ✅ Force Pion to only use `tailscale0` interface and **rewrite host candidates** to Tailscale IP
* ✅ If Tailscale isn't connected, **fail fast** with clear log/error. No more "traditional" fallback

---

## Detailed To-Do List

### 1) Go: Delete TURN Server Code + References

**Delete files/code paths:**

* Remove TURN server implementation file:
  - Delete `internal/rtcManager/stun_server.go` (provides `CreateTURNServer`, `Start`, `Stop`)

* Remove all usages of `turnServer`:
  - In `internal/rtcManager/manager.go`: remove `turnServer` field and all references (e.g., shutdown path calling `m.turnServer.Stop()`)
  - In constructors (`NewManager`, `NewManagerWithTailscale`): **do not** call `CreateTURNServer` or add any `ICEServers` with `turn:` URLs

**ICE servers config (Go, Pion):**

```go
// Tailscale-only: minimal STUN or none at all.
// (STUN is harmless; Tailscale overlay makes host candidates enough.)
pcConfig := webrtc.Configuration{
    ICEServers: []webrtc.ICEServer{
        { URLs: []string{"stun:stun.l.google.com:19302"} }, // optional
    },
    ICETransportPolicy: webrtc.ICETransportPolicyAll,
}
```

---

### 2) Go: Force ICE to Use Tailscale IP + Interface

> You already capture Tailscale's TailAddr (your `localIP`) — use it to control ICE candidates and bind to the Tailscale interface.

**In WebRTC manager initialization (where you build API with SettingEngine):**

```go
// assume you already have tm := your tailscale manager, with GetLocalTailscaleIP()
tsIP := m.tailscaleManager.GetLocalTailscaleIP()

settingEngine := webrtc.SettingEngine{}

// 1) Force gathering on tailscale interface only
settingEngine.SetInterfaceFilter(func(name string) bool {
    // Linux/Unix: "tailscale0"
    // macOS: often "utun*" but tailscale still shows as "utun..."
    return name == "tailscale0" || strings.HasPrefix(name, "utun")
})

// 2) Rewrite host candidates to Tailscale IP (pin host candidates to tailnet)
if tsIP != "" {
    settingEngine.SetNAT1To1IPs([]string{tsIP}, webrtc.ICECandidateTypeHost)
}

// (optional) 3) Restrict network types to UDP only, v4/v6 as needed
settingEngine.SetNetworkTypes([]webrtc.NetworkType{
    webrtc.NetworkTypeUDP4,
    webrtc.NetworkTypeUDP6,
})

// Then build the API with this setting engine, as you already do
api := webrtc.NewAPI(
    webrtc.WithMediaEngine(&mediaEngine),
    webrtc.WithSettingEngine(settingEngine),
)
```

This guarantees the **only** host candidates you emit are reachable **inside your tailnet**, and the ICE agent will not wander off other NICs.

---

### 3) Go: Stop Shelling Out `tailscale up`; Use Status Only

* **Rip out** any code that runs `tailscale up` via `exec.Command`. Provision Tailscale externally (your `setup-tailscale.sh` already does this).
* For status, you can keep CLI parsing:

  ```go
  exec.CommandContext(ctx, "tailscale", "status", "--json")
  ```

  or, preferably, add a small local API client (Unix socket) to query `/localapi/v0/status`.

---

### 4) Go: Remove TURN-Specific Health and Metrics

* In `internal/rtcManager/connectionHealth.go`, delete TURN metrics/warnings logic:
  - `collectTURNMetrics`
  - Any `TURNWarning` mentions (e.g., "restart TURN server or switch to backup")

* Replace with simple **Tailscale ping** checks (via CLI or Local API):
  - Periodically `tailscale ping <peer>` and record RTT in your metrics
  - Treat large/failed pings as an ICEWarning or LatencyWarning instead

---

### 5) SFU Config: Strip TURN Completely

Open `sfu.toml`:

* Remove hardcoded TURN iceserver entry:

  ```toml
  # [[webrtc.iceserver]]
  # urls = ["turn:YOUR_LOCAL_IP:3478"]
  # username = ...
  # credential = ...
  ```

  (Delete lines like these)

* Disable embedded TURN block:

  ```toml
  [turn]
  enabled = false
  ```

  (Instead of `true`)

* Keep portrange/timeouts as-is. Optional: you can add STUN if you really want, but for tailnet-only, host candidates suffice.

---

### 6) Front-End: Remove "Traditional" Fallback and TURN Hints

In `public/index-tailscale.js`:

* **Delete** `configureWebRTCTraditional()` or make it **throw a user-facing error** instructing them to connect Tailscale (no TURN fallback). It currently suggests adding TURN config.

* In `detectAndConfigureTailscale()`, **do not** call traditional config on failure; instead:

  ```js
  updateStatus('🔴 Tailscale not detected. Please connect to your tailnet and reload.');
  throw new Error('Tailscale not connected');
  ```

  (It currently falls back to traditional)

* Keep your logging/status UI; it's solid for troubleshooting.

---

### 7) Node Proxy: Always Use Tailscale-Optimized Ion URL

In `server-tailscale.js`:

* On startup, **initialize Tailscale** and **replace** the Ion connection URL with `tailscale.getOptimizedIonSfuUrl()`; that logic is already implemented.

* Keep the CSP expansion and auth middleware; those are good protections for tailnet traffic.

*(Nice touch: the CSP builder dynamically allows peers and the local tail IP when connected.)*

---

### 8) Config & Validation: Require Tailscale, Drop TURN Creds

* In your config validation, ensure **TailscaleConfig.Enabled == true** and required fields are set. Remove **TURN/WebRTC auth** requirements entirely.

* Remove any example/required fields in docs referencing TURN credentials. Update `TAILSCALE_README.md` to say "no TURN needed, tailnet-only" (it currently describes both modes).

---

### 9) (Optional but Recommended) Expose UI via Tailscale Serve/Funnel

Operational sugar, not code:

```bash
# serve UI over Tailscale
tailscale serve http 3000

# if you want global access via tailnet funnel (requires funnel enabled)
tailscale funnel 3000
```

This gives you HTTPS + auth "for free" over your tailnet, and you can stop poking firewall holes.

---

### 10) Put `tm.localIP` to Work (You Left It on the Table)

You gathered the Tailscale TailAddr (your Tailnet IP) but never used it. The ICE pinning above explicitly uses it via `SetNAT1To1IPs`. That's the missing piece that wires `tm.localIP` into candidate gathering.

---

### 11) Sanity Sweep (Find/Delete Stragglers)

* Remove any references to `turn:` URLs in code comments (FE config hints) and examples
* Remove the `turnServer` fields/structs/imports from Manager types and constructors
* Remove any Docker/compose or script steps that start TURN or expose 3478/49xxx ports

---

## Quick Patch Examples (Drop-In)

### A) Remove Traditional Fallback in FE

```diff
- // Fall back to traditional configuration
- configureWebRTCTraditional();
- logDebug("Using traditional WebRTC networking");
- return false;
+ updateStatus('🔴 Tailscale not detected. Please connect to your tailnet and reload.');
+ throw new Error('Tailscale not connected');
```

(Where `detectAndConfigureTailscale()` falls back now)

### B) Disable TURN in sfu.toml

```diff
- urls = ["turn:YOUR_LOCAL_IP:3478"]
- username = "camera_user"
- credential = "change-this-password"
...
- [turn]
- enabled = true
+ [turn]
+ enabled = false
```

### C) Kill TURN Health Probes

```diff
- func (cd *ConnectionDoctor) collectTURNMetrics(metrics *QualityMetrics) {
-     ...
-     cd.warnings <- Warning{ Type: TURNWarning, ... }
-     ...
- }
```

### D) Manager Shutdown Shouldn't Mention TURN

```diff
- if m.turnServer != nil {
-     if err := m.turnServer.Stop(); err != nil {
-         log.Printf("Failed to stop TURN server: %v", err)
-     }
- }
```

---

## Bonus: Tiny Go Helper to Ensure TS is Present Before Starting WebRTC

```go
func requireTailscale(tm *tailscale.TailscaleManager) error {
    ip := tm.GetLocalTailscaleIP()
    if net.ParseIP(ip) == nil {
        return fmt.Errorf("tailscale not connected (no tailnet IP)")
    }
    return nil
}
```

Call this once after `NewTailscaleManager`. If it fails, **exit** with a helpful error.

---

## TL;DR

* **Delete TURN everywhere.**
* **Pin ICE to `tailscale0`** and **rewrite host candidates** to your TailAddr (this is where your `tm.localIP` finally matters).
* **Require Tailscale** and stop shelling out `tailscale up` from Go.
* **Front-end must not silently fallback**; instruct the user to connect to the tailnet.
* **SFU config: TURN off; no turn: URLs.**
* **Keep your Node proxy's Tailscale optimization; use it by default.**

---

## File Locations Reference

- `internal/rtcManager/stun_server.go` - DELETE
- `internal/rtcManager/manager.go` - Remove turnServer field
- `internal/rtcManager/connectionHealth.go` - Remove TURN metrics
- `sfu.toml` - Disable TURN
- `public/index-tailscale.js` - Remove fallback
- `server-tailscale.js` - Already good, keep using
- `TAILSCALE_README.md` - Update to reflect tailnet-only approach
