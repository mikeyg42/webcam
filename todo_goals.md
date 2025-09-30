# Webcam Security System - Todo & Goals

## Current Status: Email Notification System Optimization & Modularization

### Phase 1: MailerSend Service Optimization
- [ ] **Research MailerSend optimal configuration**
  - [ ] Determine if API vs SMTP relay is better for our use case
  - [ ] Minimize required MailerSend dashboard configuration
  - [ ] Ensure "clone and run" simplicity (minimal external setup)
  - [ ] Document recommended MailerSend token permissions (minimize scope)
  - [ ] Evaluate domain verification requirements
  - [ ] Consider sender identity configuration

### Phase 2: Gmail OAuth2 Integration & Code Modularization
- [ ] **Review and integrate Gmail notifier implementation**
  - [ ] Validate Gmail OAuth2 implementation security and best practices
  - [ ] Add missing dependencies to go.mod (oauth2, gmail API)
  - [ ] Create shared email template system for code reuse
  - [ ] Extract common retry logic into shared utilities
  - [ ] Standardize error handling patterns across both notifiers

- [ ] **Modularize notification package**
  - [ ] Extract shared email template functionality
  - [ ] Create common retry mechanism (used by both Gmail & MailerSend)
  - [ ] Unify configuration patterns between Gmail and MailerSend
  - [ ] Create shared email data preparation functions
  - [ ] Ensure consistent interface implementations
  - [ ] Add unified debug/testing capabilities

### Phase 3: Email Configuration GUI (NEW PRIORITY)
- [ ] **Research and choose GUI framework for Go**
  - [ ] Evaluate Fyne vs Gio vs embedded web server approach
  - [ ] Consider cross-platform compatibility (Windows/Mac/Linux)
  - [ ] Test basic GUI window creation and user interactions

- [ ] **Create Email Setup Dialog**
  - [ ] Design main configuration window with 3 clear options
  - [ ] Add MailerSend token input field with validation
  - [ ] Add Gmail OAuth2 button with progress indication
  - [ ] Add "Skip Email" option that continues without notifications
  - [ ] Implement window styling and responsive layout

- [ ] **Implement Email Method Testing**
  - [ ] Create unified test email function for both methods
  - [ ] Add real-time validation feedback (spinner, success/error states)
  - [ ] Implement retry logic with specific error messages
  - [ ] Add method switching without restarting dialog

- [ ] **Error Handling & Recovery System**
  - [ ] Design error display with actionable messages
  - [ ] Implement persistent dialog (stays open until valid choice)
  - [ ] Add method fallback options (try different method on failure)
  - [ ] Create configuration persistence for successful setups

### Phase 4: Integration with Main Application
- [ ] **Update main application startup flow**
  - [ ] Show GUI before camera initialization
  - [ ] Wait for email method selection/configuration
  - [ ] Initialize appropriate notifier based on GUI choice
  - [ ] Add headless mode flag for server deployments

- [ ] **Documentation & Setup**
  - [ ] Update .env.example with Gmail OAuth2 options
  - [ ] Create Gmail OAuth2 setup guide (Google Cloud Console steps)
  - [ ] Document MailerSend vs Gmail trade-offs
  - [ ] Add troubleshooting guide for both email methods
  - [ ] Update SETUP.md with new email configuration options

### Phase 4: Testing & Validation
- [ ] **Test both email notification systems**
  - [ ] Verify MailerSend integration works with optimized configuration
  - [ ] Test Gmail OAuth2 flow end-to-end
  - [ ] Validate encrypted token storage and retrieval
  - [ ] Test email template rendering for both systems
  - [ ] Verify retry logic and error handling
  - [ ] Test "clone and run" experience for both email methods

### Design Goals
1. **Maximum Code Reuse**: Share templates, retry logic, and utilities between Gmail & MailerSend
2. **Consistent Interface**: Both notifiers should implement identical interfaces
3. **"Clone and Run" Friendly**: Minimal external service configuration required
4. **Security First**: Encrypted token storage, minimal OAuth scopes, secure defaults
5. **Graceful Degradation**: Core motion detection works even if email fails
6. **Developer Experience**: Clear configuration, good error messages, easy debugging

### Future Considerations
- [ ] Consider adding SMS notifications (Twilio integration)
- [ ] Webhook notification support for external systems
- [ ] Push notification support (Firebase/Apple Push)
- [ ] Multiple recipient support
- [ ] HTML email templates with embedded images
- [ ] **Support for headless browsers (use the device code OAuth flow)** - Very low priority for server deployments without GUI support