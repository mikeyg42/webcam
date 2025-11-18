#ifndef AVFOUNDATION_PERMISSIONS_H
#define AVFOUNDATION_PERMISSIONS_H

// Authorization status constants (matching AVAuthorizationStatus)
// 0 = NotDetermined, 1 = Restricted, 2 = Denied, 3 = Authorized
int CheckCameraAuthorizationStatus();
int CheckMicrophoneAuthorizationStatus();

// Request access (returns 1 if granted, 0 if denied, -1 on timeout)
int RequestCameraAccess();
int RequestMicrophoneAccess();

#endif
