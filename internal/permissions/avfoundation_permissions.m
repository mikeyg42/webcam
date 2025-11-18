#import <AVFoundation/AVFoundation.h>
#import <Foundation/Foundation.h>
#include "avfoundation_permissions.h"

// Check authorization status for camera
int CheckCameraAuthorizationStatus() {
    AVAuthorizationStatus status = [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeVideo];
    return (int)status;
}

// Check authorization status for microphone
int CheckMicrophoneAuthorizationStatus() {
    AVAuthorizationStatus status = [AVCaptureDevice authorizationStatusForMediaType:AVMediaTypeAudio];
    return (int)status;
}

// Request camera access (blocks until user responds)
int RequestCameraAccess() {
    __block int result = 0;
    __block BOOL completed = NO;
    
    dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);
    
    [AVCaptureDevice requestAccessForMediaType:AVMediaTypeVideo completionHandler:^(BOOL granted) {
        result = granted ? 1 : 0;
        completed = YES;
        dispatch_semaphore_signal(semaphore);
    }];
    
    // Wait for user response (with timeout)
    dispatch_time_t timeout = dispatch_time(DISPATCH_TIME_NOW, (int64_t)(60.0 * NSEC_PER_SEC));
    long timeoutResult = dispatch_semaphore_wait(semaphore, timeout);
    
    if (timeoutResult != 0) {
        // Timeout occurred
        return -1;
    }
    
    return result;
}

// Request microphone access (blocks until user responds)
int RequestMicrophoneAccess() {
    __block int result = 0;
    __block BOOL completed = NO;
    
    dispatch_semaphore_t semaphore = dispatch_semaphore_create(0);
    
    [AVCaptureDevice requestAccessForMediaType:AVMediaTypeAudio completionHandler:^(BOOL granted) {
        result = granted ? 1 : 0;
        completed = YES;
        dispatch_semaphore_signal(semaphore);
    }];
    
    // Wait for user response (with timeout)
    dispatch_time_t timeout = dispatch_time(DISPATCH_TIME_NOW, (int64_t)(60.0 * NSEC_PER_SEC));
    long timeoutResult = dispatch_semaphore_wait(semaphore, timeout);
    
    if (timeoutResult != 0) {
        // Timeout occurred
        return -1;
    }
    
    return result;
}
