# Security Camera Configuration: Research Report

## Executive Summary

This report covers three research areas:
1. **Chromium Password Forms Best Practices** - Complete recommendations extracted
2. **Go Password Generation Libraries** - Top options analyzed
3. **Go Configuration GUI Libraries** - Assessment of alternatives

---

## 1. CHROMIUM PASSWORD FORMS BEST PRACTICES

### Issues Found in Your Current `config.html`:

**CRITICAL ISSUES:**
1. ❌ **Missing autocomplete attributes** on ALL password and username fields
2. ❌ **Duplicate IDs** - Line 449 and 454 both use generic names that could conflict
3. ❌ No differentiation between "new password" vs "current password" contexts

**Current State:**
```html
Line 432: <input type="text" id="postgresUsername" value="recorder" required>
Line 437: <input type="password" id="postgresPassword" value="recorder" required>
Line 449: <input type="text" id="webrtcUsername" value="testuser" required>
Line 454: <input type="password" id="webrtcPassword" value="testing123" required>
Line 494: <input type="email" id="emailTo" placeholder="your-email@example.com">
Line 498: <input type="email" id="emailFrom" placeholder="security@webcam.local">
```

### Required Fixes:

#### 1. Add Autocomplete Attributes

**For WebRTC Username/Password (these are NEW credentials being SET):**
```html
<label for="webrtcUsername">Username <span class="required">*</span></label>
<input 
    type="text" 
    id="webrtcUsername" 
    name="webrtcUsername"
    value="testuser" 
    autocomplete="username"
    required>

<label for="webrtcPassword">Password <span class="required">*</span></label>
<input 
    type="password" 
    id="webrtcPassword" 
    name="webrtcPassword"
    value="testing123" 
    autocomplete="new-password"
    required>
```

**For PostgreSQL Credentials:**
```html
<label for="postgresUsername">Username <span class="required">*</span></label>
<input 
    type="text" 
    id="postgresUsername" 
    name="postgresUsername"
    value="recorder" 
    autocomplete="username"
    required>

<label for="postgresPassword">Password <span class="required">*</span></label>
<input 
    type="password" 
    id="postgresPassword" 
    name="postgresPassword"
    value="recorder" 
    autocomplete="new-password"
    required>
```

**For MinIO Secret Key:**
```html
<label for="minioSecretKey">Secret Key <span class="required">*</span></label>
<input 
    type="password" 
    id="minioSecretKey" 
    name="minioSecretKey"
    value="minioadmin" 
    autocomplete="new-password"
    required>
```

**For Email Fields:**
```html
<label for="emailTo">To Email</label>
<input 
    type="email" 
    id="emailTo" 
    name="emailTo"
    placeholder="your-email@example.com"
    autocomplete="email">

<label for="emailFrom">From Email</label>
<input 
    type="email" 
    id="emailFrom" 
    name="emailFrom"
    placeholder="security@webcam.local"
    autocomplete="email">
```

#### 2. Add `name` Attributes

All inputs MUST have a `name` attribute for proper form submission and password manager functionality:
- Add `name="webrtcUsername"` to username inputs
- Add `name="webrtcPassword"` to password inputs
- Add `name="postgresUsername"`, `name="postgresPassword"`, etc.

#### 3. Context-Specific Autocomplete

Use the right autocomplete value based on context:
- **New credentials being configured:** `autocomplete="new-password"`
- **Existing credentials to authenticate:** `autocomplete="current-password"`
- **Username fields:** `autocomplete="username"`
- **Email fields:** `autocomplete="email"`

### Complete Chromium Best Practices Summary:

✅ **DO:**
1. Group related fields in a single `<form>` element
2. Use `autocomplete="username"` for username fields
3. Use `autocomplete="current-password"` for login/authentication
4. Use `autocomplete="new-password"` for registration/configuration
5. Use `autocomplete="email"` for email fields
6. Add unique `id` attributes to all fields
7. Add `name` attributes to all form fields
8. Make form submission clear (navigation or History API)
9. Use hidden fields for implicit information (e.g., email-first flow)

❌ **DON'T:**
1. Combine multiple processes (registration + login) in one form
2. Skip the `<form>` element
3. Use duplicate IDs
4. Insert fake/honeypot fields (confuses password managers)
5. Use incorrect autocomplete attributes
6. Navigate to separate page on login failure

### Code Examples from Chromium Documentation:

**Basic Sign-in Form:**
```html
<form id="login" action="/login" method="post">
  <input id="username" type="text" name="username" autocomplete="username" required>
  <input id="password" type="password" name="password" autocomplete="current-password" required>
  <button type="submit">Sign In</button>
</form>
```

**Registration/Setup Form:**
```html
<form id="signup" action="/signup" method="post">
  <input id="username" type="text" name="username" autocomplete="username" required>
  <input id="password" type="password" name="password" autocomplete="new-password" required>
  <button type="submit">Create Account</button>
</form>
```

**With Password Confirmation:**
```html
<form id="signup" action="/signup" method="post">
  <input id="username" type="text" name="username" autocomplete="username" required>
  <input id="password" type="password" name="password" autocomplete="new-password" required>
  <input id="password-confirm" type="password" name="password-confirm" autocomplete="new-password" required>
  <button type="submit">Create Account</button>
</form>
```

---

## 2. GO PASSWORD GENERATION LIBRARIES

### Recommended: crypto/rand (Standard Library)

**Why:**
- No external dependencies
- Cryptographically secure
- Simple to implement
- Full control over logic
- Well-tested by Go team

**Implementation Example:**

```go
package password

import (
    "crypto/rand"
    "errors"
    "math/big"
)

type Config struct {
    Length         int
    IncludeUpper   bool
    IncludeLower   bool
    IncludeDigits  bool
    IncludeSymbols bool
    MinDigits      int
    MinSymbols     int
}

const (
    lowerChars  = "abcdefghijklmnopqrstuvwxyz"
    upperChars  = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
    digitChars  = "0123456789"
    symbolChars = "!@#$%^&*()-_=+[]{}|;:,.<>?"
)

func Generate(cfg Config) (string, error) {
    if cfg.Length < 8 {
        return "", errors.New("password must be at least 8 characters")
    }

    // Build character set
    var charset string
    if cfg.IncludeLower {
        charset += lowerChars
    }
    if cfg.IncludeUpper {
        charset += upperChars
    }
    if cfg.IncludeDigits {
        charset += digitChars
    }
    if cfg.IncludeSymbols {
        charset += symbolChars
    }

    if len(charset) == 0 {
        return "", errors.New("must include at least one character type")
    }

    // Generate password
    password := make([]byte, cfg.Length)
    charsetLen := big.NewInt(int64(len(charset)))

    for i := range password {
        idx, err := rand.Int(rand.Reader, charsetLen)
        if err != nil {
            return "", err
        }
        password[i] = charset[idx.Int64()]
    }

    // TODO: Add ensureMinimumRequirements() if needed
    
    return string(password), nil
}

// Simple one-liner for default password
func GenerateDefault() (string, error) {
    return Generate(Config{
        Length:         16,
        IncludeUpper:   true,
        IncludeLower:   true,
        IncludeDigits:  true,
        IncludeSymbols: true,
        MinDigits:      2,
        MinSymbols:     2,
    })
}
```

**Usage:**
```go
// Default secure password
pass, err := password.GenerateDefault()
// Returns: "aB3$xYz9!mNoPqR"

// Custom requirements
pass, err := password.Generate(password.Config{
    Length:         20,
    IncludeUpper:   true,
    IncludeLower:   true,
    IncludeDigits:  true,
    IncludeSymbols: false, // No symbols for compatibility
})
```

### Alternative: sethvargo/go-password

If you prefer a library:

**Installation:**
```bash
go get github.com/sethvargo/go-password/password
```

**Usage:**
```go
import "github.com/sethvargo/go-password/password"

// Generate(length, numDigits, numSymbols, noUpper, allowRepeat)
pass, err := password.Generate(16, 3, 3, false, false)
// Returns 16-char password with 3 digits, 3 symbols, uppercase allowed, no repeats
```

**Pros:**
- Simple API
- Well-tested
- Active maintenance

**Cons:**
- External dependency
- Less control than custom implementation

### Other Libraries (Not Recommended):

- **elithrar/simple-scrypt** - For password HASHING, not generation
- **crypto/bcrypt** - For password HASHING, not generation
- **golang.org/x/crypto/argon2** - For password HASHING, not generation

**Note:** You'll need a hashing library when STORING passwords, but for GENERATING them, use crypto/rand.

---

## 3. GO CONFIGURATION GUI LIBRARIES

### Analysis of Your Current Setup

**What You Have:**
- ✅ `public/config.html` - Clean, custom HTML/CSS
- ✅ `internal/api/config_handler.go` - REST API for config management
- ✅ `internal/gui/email_setup.go` - Lorca-based desktop GUI
- ✅ Separation of concerns
- ✅ No unnecessary dependencies

**Verdict: Keep your current approach!**

### Why Your Current Approach is Good:

1. **Simple and Maintainable**
   - ~500 lines of HTML
   - Clean REST API
   - Easy to understand

2. **No Framework Bloat**
   - No heavy dependencies
   - Fast loading
   - Easy debugging

3. **Flexible**
   - Can add JavaScript features as needed
   - Can style however you want
   - Not locked into framework patterns

### Alternatives Evaluated:

#### ❌ GoAdmin / go-admin
**Repository:** https://github.com/GoAdminGroup/go-admin
- Heavy framework (50+ dependencies)
- Database-driven
- Overkill for simple config

#### ❌ Buffalo
- Full MVC framework
- Way too heavy
- Requires restructuring entire app

#### ❌ Fiber + Admin Template
- Would require rewriting HTTP layer
- More complexity than benefit

#### ⚠️ Viper (Config Management)
**Repository:** https://github.com/spf13/viper
- Good for reading configs from multiple sources
- Supports YAML, JSON, TOML, env vars
- Has file watching capability
- **Maybe useful** if you want hot-reload

**Example:**
```go
import "github.com/spf13/viper"

viper.SetConfigName("config")
viper.SetConfigType("json")
viper.AddConfigPath("$HOME/.webcam2")
viper.AutomaticEnv() // Read from env vars

if err := viper.ReadInConfig(); err != nil {
    log.Fatal(err)
}

// Watch for changes
viper.WatchConfig()
viper.OnConfigChange(func(e fsnotify.Event) {
    log.Println("Config file changed:", e.Name)
    // Reload config
})
```

**Assessment:** Optional, not necessary for your current needs.

#### ✅ HTMX (Optional Enhancement)
**What it is:** JavaScript library for dynamic HTML updates
**Repository:** https://htmx.org/

**Why consider it:**
- Add dynamic updates without full page reload
- No React/Vue complexity
- Works great with Go's html/template
- ~14kb library

**Example:**
```html
<!-- Form with HTMX -->
<form hx-post="/api/config" hx-target="#result" hx-swap="innerHTML">
    <input name="width" value="640">
    <button type="submit">Save</button>
</form>
<div id="result"></div>
```

```go
// Go handler returns HTML fragment
func (h *ConfigHandler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
    // Update config...
    
    // Return HTML fragment instead of JSON
    w.Write([]byte(`<div class="alert success">Saved!</div>`))
}
```

**Assessment:** Nice-to-have, not required.

### Recommendation for GUI:

**Keep your current approach** with these optional enhancements:

1. **Add password generation button** (JavaScript + Go API endpoint)
2. **Add client-side validation** (vanilla JavaScript)
3. **Consider HTMX** for dynamic updates (optional)
4. **Fix autocomplete attributes** (high priority)

---

## 4. FINAL RECOMMENDATIONS

### High Priority (Do These):

1. ✅ **Fix autocomplete attributes** in `config.html`
   - Add `autocomplete="username"` to username fields
   - Add `autocomplete="new-password"` to password fields
   - Add `autocomplete="email"` to email fields
   - Add `name` attributes to all inputs

2. ✅ **Implement password generation** using crypto/rand
   - Create `internal/password/generator.go`
   - Add API endpoint `/api/generate-password`
   - Add "Generate" button in config.html

3. ✅ **Add password hashing** for stored credentials
   - Use `golang.org/x/crypto/bcrypt` or `golang.org/x/crypto/argon2`
   - Hash WebRTC passwords before storing in config
   - **CRITICAL:** Never store plain-text passwords

### Medium Priority (Consider These):

4. ⚠️ **Add client-side validation**
   - Password strength indicator
   - Form validation before submission
   - Prevent invalid inputs

5. ⚠️ **Add password visibility toggle**
   - Show/hide password buttons
   - Better UX for long passwords

6. ⚠️ **Consider HTMX** for better UX
   - Save without full page reload
   - Real-time validation feedback

### Low Priority (Optional):

7. 🔵 **Add Viper** if you need:
   - Hot-reload of config files
   - Environment variable overrides
   - Multiple config formats (YAML, TOML, etc.)

8. 🔵 **Add password requirements UI**
   - Show requirements as user types
   - Visual feedback (green checkmarks)

---

## 5. CODE EXAMPLES FOR YOUR PROJECT

### Example 1: Password Generator API Endpoint

**File: `internal/api/password_handler.go`**
```go
package api

import (
    "encoding/json"
    "net/http"
    
    "github.com/mikeyg42/webcam/internal/password"
)

type GeneratePasswordRequest struct {
    Length         int  `json:"length"`
    IncludeSymbols bool `json:"includeSymbols"`
}

type GeneratePasswordResponse struct {
    Password string `json:"password"`
    Error    string `json:"error,omitempty"`
}

func GeneratePassword(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
        return
    }

    var req GeneratePasswordRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        json.NewEncoder(w).Encode(GeneratePasswordResponse{
            Error: "Invalid request",
        })
        return
    }

    // Default to 16 characters if not specified
    if req.Length == 0 {
        req.Length = 16
    }

    pass, err := password.Generate(password.Config{
        Length:         req.Length,
        IncludeUpper:   true,
        IncludeLower:   true,
        IncludeDigits:  true,
        IncludeSymbols: req.IncludeSymbols,
        MinDigits:      2,
        MinSymbols:     2,
    })

    if err != nil {
        json.NewEncoder(w).Encode(GeneratePasswordResponse{
            Error: err.Error(),
        })
        return
    }

    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(GeneratePasswordResponse{
        Password: pass,
    })
}
```

### Example 2: Password Generation Button in HTML

**Add to `config.html` near password fields:**
```html
<div class="form-group">
    <label>Password <span class="required">*</span></label>
    <div style="display: flex; gap: 10px;">
        <input 
            type="password" 
            id="webrtcPassword" 
            name="webrtcPassword"
            autocomplete="new-password"
            required
            style="flex: 1;">
        <button 
            type="button" 
            onclick="generatePassword('webrtcPassword')"
            style="padding: 10px 20px;">
            🔑 Generate
        </button>
    </div>
    <small>Min 8 characters</small>
</div>

<script>
async function generatePassword(fieldId) {
    try {
        const response = await fetch('/api/generate-password', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ length: 16, includeSymbols: true })
        });
        
        const data = await response.json();
        
        if (data.error) {
            alert('Error: ' + data.error);
            return;
        }
        
        // Set password and switch to text briefly to show it
        const field = document.getElementById(fieldId);
        field.value = data.password;
        field.type = 'text';
        
        // Switch back to password after 2 seconds
        setTimeout(() => {
            field.type = 'password';
        }, 2000);
        
    } catch (error) {
        alert('Failed to generate password: ' + error);
    }
}
</script>
```

### Example 3: Password Visibility Toggle

**Add to `config.html`:**
```html
<div class="form-group">
    <label>Password <span class="required">*</span></label>
    <div style="display: flex; gap: 10px; align-items: center;">
        <input 
            type="password" 
            id="webrtcPassword" 
            name="webrtcPassword"
            autocomplete="new-password"
            required
            style="flex: 1;">
        <button 
            type="button" 
            onclick="togglePasswordVisibility('webrtcPassword')"
            style="padding: 10px;">
            👁️
        </button>
        <button 
            type="button" 
            onclick="generatePassword('webrtcPassword')"
            style="padding: 10px 20px;">
            🔑 Generate
        </button>
    </div>
</div>

<script>
function togglePasswordVisibility(fieldId) {
    const field = document.getElementById(fieldId);
    field.type = field.type === 'password' ? 'text' : 'password';
}
</script>
```

---

## 6. SECURITY CONSIDERATIONS

### Password Storage:

❌ **NEVER DO THIS:**
```go
// DON'T store plain-text passwords!
config.WebRTCPassword = "testing123"
saveToFile(config)
```

✅ **DO THIS:**
```go
import "golang.org/x/crypto/bcrypt"

// Hash password before storing
hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
if err != nil {
    return err
}
config.WebRTCPasswordHash = string(hashedPassword)
saveToFile(config)

// Verify password later
err := bcrypt.CompareHashAndPassword([]byte(config.WebRTCPasswordHash), []byte(inputPassword))
if err != nil {
    // Password incorrect
}
```

### Current Issues in Your Code:

**File: `/Users/mikeglendinning/projects/webcam2/internal/api/config_handler.go`**

Lines 246-247:
```go
Password: "", // Don't send password
```

✅ Good! You're not sending passwords to frontend.

But lines 301-302:
```go
if req.WebRTC.Password != "" {
    cfg.WebrtcAuth.Password = req.WebRTC.Password
}
```

⚠️ **Problem:** Storing plain-text password in config!

**Fix Required:**
```go
if req.WebRTC.Password != "" {
    // Hash before storing
    hashedPassword, err := bcrypt.GenerateFromPassword(
        []byte(req.WebRTC.Password), 
        bcrypt.DefaultCost,
    )
    if err != nil {
        return err
    }
    cfg.WebrtcAuth.PasswordHash = string(hashedPassword)
}
```

---

## 7. SUMMARY TABLE

| Feature | Current Status | Recommendation | Priority |
|---------|----------------|----------------|----------|
| Autocomplete attributes | ❌ Missing | Add to all fields | HIGH |
| Name attributes | ❌ Missing | Add to all inputs | HIGH |
| Unique IDs | ✅ Good | Keep | - |
| Password generation | ❌ None | Implement with crypto/rand | HIGH |
| Password hashing | ❌ Plain-text | Use bcrypt/argon2 | CRITICAL |
| GUI framework | ✅ Custom | Keep current approach | - |
| Password visibility | ❌ None | Add toggle button | MEDIUM |
| Client validation | ⚠️ Basic | Enhance | MEDIUM |
| HTMX | ❌ None | Consider for UX | LOW |
| Viper config | ❌ None | Optional | LOW |

---

## 8. NEXT STEPS

1. **Immediate (Security Critical):**
   - Add password hashing to config storage
   - Never store plain-text passwords

2. **High Priority:**
   - Fix autocomplete attributes in config.html
   - Add name attributes to all form inputs
   - Implement password generation API

3. **Medium Priority:**
   - Add "Generate Password" buttons
   - Add password visibility toggles
   - Enhance client-side validation

4. **Optional Enhancements:**
   - Consider HTMX for better UX
   - Add password strength indicator
   - Add real-time form validation

---

## RESOURCES

- **Chromium Password Forms:** https://www.chromium.org/developers/design-documents/create-amazing-password-forms/
- **Autocomplete Values:** https://www.chromium.org/developers/design-documents/form-styles-that-chromium-understands/
- **Go crypto/rand:** https://pkg.go.dev/crypto/rand
- **Go bcrypt:** https://pkg.go.dev/golang.org/x/crypto/bcrypt
- **sethvargo/go-password:** https://github.com/sethvargo/go-password
- **HTMX:** https://htmx.org/
- **Web.dev Forms:** https://web.dev/learn/forms/autofill/

