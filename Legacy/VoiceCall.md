# New feature request: Voice Call.

Make a voice call webapp, hosted on another port

## Expectation
User can call people by entering their user name, the person being called will have a banner displayed, and the option button to accept/decline
There should also be a "Calls" section that shows recent calls.

## Protocol
WebRTC is obviously the best choice to do that, make sure you handle the protocol, state and mapping correctly.

## Notification API
Consider to use Web notification API for incoming call notification when the tab is switched to the background or becomes invisible, Page Visibility API can be used to detect visibility state.

## Ring tone
For incoming call ringtone, please use the mp3 file in sound/RingTone.mp3

## UI
Precisely follow the UI design of the main product. Make sure it looks coherent, concise and elegant, verify that yourself.
The UI should also handle edge cases, for example, when the incoming call is cancelled, the banner should disappear, there should also be appropirate notification when outcoming call is declined.

Also, make sure you handle Synchronization correctly
