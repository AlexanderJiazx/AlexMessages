# Bugs

The current version of AlexMeet is extremely buggy, full of different kind of problems.

### Multi-entry for single user
When the user quit in a abnormal way (eg. network interruption) and rejoin the meeting, there will be 2 instance of this user in the meeting.
And if user is the host, they will lose the host right, because the ghost user instance is holding it.
User should only be able to present in the same meeting as one user, they can join more than one meetings simultaneously, but being able to join the meeting with 2 instance is definitely a bug
Joining from other session is allowed but new instance should replace the old one (and inherit the host right when feasible)

### Interruption sensitivity
Connection is extremely easy to be interrupted, even if the network is connected.
Once there's an interruption, the client (frontend) will immediately stop and display the interrupted message, with no retrying to connect feature at all
The expected behavior is, client should at least retry for a while, and timeout when lose connection for over 10 seconds. Server should also handle reconnection gracefully.

### Streaming quality memory doesn’t work
The client remembers what quality user selected last time and will reuse next time in UI, but the reuse only happen in UI, when user enter a new meeting, although the UI is showing the quality is set to their last time choice, the actual quality is still default. The memorized config wasn't actually being used.

### Ratio on phone
There's a misunderstanding when building this app, the app should choose the content display/arrangement based on the *ratio* of the window, not purely width or something, for example, in non-grid (highlighted) view, on low resolution device like phone, even when used horizontally, the participants preview stack will still be placed below the highlighted particpants image, making the highlighted window squizzed to a thin window because of that (the correct behavior is to place the participants preview on the right for horizontal display).

### Volume
User reports the volume feels too low, maybe add a volume slider in mic pop up

### Audio Video out of sync
Some user reported the out of sync issue between audio/video, think a way to ensure audio and video are always in sync

### Microphone issue
Some user reported they cannot use their microphone for unknown reason.

### Re-asking permission
In the preview phase before actually joining the meeting, it already asked the audio/video permission, and it should not ask it again when clicking join to join the meeting.

## Feature request
There are also some new feature request that can make a quality of life difference
### Scale to fill instead of scale to fit
User should be able to choose the window fit/fill method in non-grid view using the button on the bottom-left corner, switch whether scale to fit the window bound, or scale to fill the window (scale to fill should be the default), instead of the force scale to fit for all like it currently does.
### Full screen
User should be able to full screen a view by clicking the full screen button (icon only) on the top-left corner of particpants window (to be clear, only highlighted participants window should have that button), the fit/fill choice inherit and still apply in full screen.

# Note
All icon should still use embedded Lucide SVGs.
You should be honest on development, when you are not sure about something, you can ask me, but not proceed with assumptions.
You can ask the dev to do a specific test to validate your idea, but do NOT ask it too many times, you should describe all of your needs at once
For each fixes, evaluate your confidence about your fix.